package localserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/githubstatus"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/passportrepo"
)

type ignoredRunner struct{}

func (ignoredRunner) Run(_ context.Context, _ string, _ string, _ ...string) ([]byte, error) {
	return []byte{}, nil
}

type unavailableGitHub struct{}

func (unavailableGitHub) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return nil, context.DeadlineExceeded
}

func testServer(t *testing.T) *Server {
	t.Helper()
	repository := passportrepo.Repository{
		Root: t.TempDir(), Owner: "student", Name: "passport-exercises", UpstreamOwner: "soheylm-passport-sandbox", UpstreamName: "passport-exercises", Branch: "onboarding/student",
		HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Passport: passportrepo.Passport{
			SchemaVersion: 2, CurriculumVersion: "1.2.0", GitHubUser: "student",
			SourceRepository: "soheylm-passport-sandbox/passport-exercises", ForkRepository: "student/passport-exercises", AssessmentBranch: "onboarding/student",
			Platform: "linux", Missions: []string{"core-orientation"},
		},
	}
	server, err := New(repository, 42, unavailableGitHub{}, ignoredRunner{})
	if err != nil {
		t.Fatal(err)
	}
	server.origin = "http://127.0.0.1:43127"
	return server
}

func request(server *Server, method, path string, body []byte, session, origin bool) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, server.origin+path, bytes.NewReader(body))
	if session {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: server.token})
	}
	if origin {
		req.Header.Set("Origin", server.origin)
	}
	server.handler().ServeHTTP(recorder, req)
	return recorder
}

func TestAPIsRequireSessionAndMutationOrigin(t *testing.T) {
	server := testServer(t)
	response := request(server, http.MethodGet, "/__passport/v1/context", nil, false, false)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("context without session returned %d", response.Code)
	}

	response = request(server, http.MethodGet, "/__passport/v1/context", nil, true, false)
	var payload ContextPayload
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Mode != "local" || payload.Official != nil || payload.Repository.Root != "" {
		t.Fatalf("unsafe context payload: %#v", payload)
	}

	body, _ := json.Marshal(payload.LocalState)
	response = request(server, http.MethodPut, "/__passport/v1/state", body, true, false)
	if response.Code != http.StatusForbidden {
		t.Fatalf("mutation without origin returned %d", response.Code)
	}
	response = request(server, http.MethodPut, "/__passport/v1/state", body, true, true)
	if response.Code != http.StatusOK {
		t.Fatalf("same-origin state write returned %d: %s", response.Code, response.Body.String())
	}
}

func TestBrowserCannotForgeRouteOrOfficialSyncFields(t *testing.T) {
	server := testServer(t)
	contextResponse := request(server, http.MethodGet, "/__passport/v1/context", nil, true, false)
	var payload ContextPayload
	if err := json.NewDecoder(contextResponse.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}

	outside := payload.LocalState
	outside.LastOpenedMission = "not-assigned"
	body, _ := json.Marshal(outside)
	response := request(server, http.MethodPut, "/__passport/v1/state", body, true, true)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("out-of-route navigation returned %d", response.Code)
	}

	forged := payload.LocalState
	forged.LastOfficialSync = "2026-08-30T12:00:00Z"
	forged.LastSeenHeadSHA = strings.Repeat("a", 40)
	body, _ = json.Marshal(forged)
	response = request(server, http.MethodPut, "/__passport/v1/state", body, true, true)
	if response.Code != http.StatusForbidden {
		t.Fatalf("forged sync metadata returned %d", response.Code)
	}
}

func TestHostGuardRejectsDNSRebindingHost(t *testing.T) {
	server := testServer(t)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, server.origin+"/__passport/v1/health", nil)
	req.Host = "attacker.example"
	server.handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusMisdirectedRequest {
		t.Fatalf("unexpected host returned %d", recorder.Code)
	}
}

func TestForgedDiskCacheIsNeverReturnedAsOfficial(t *testing.T) {
	server := testServer(t)
	forged := githubstatus.Official{Source: "github_controller_check", ControllerAppID: 42}
	if err := server.store.SaveStatus(forged); err != nil {
		t.Fatal(err)
	}
	response := request(server, http.MethodGet, "/__passport/v1/context", nil, true, false)
	var payload ContextPayload
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Official != nil {
		t.Fatal("learner-editable status cache was treated as official")
	}
}

func TestSecurityHeadersAndStaticPathBoundary(t *testing.T) {
	server := testServer(t)
	response := request(server, http.MethodGet, "/../../passport.json", nil, false, false)
	if response.Code == http.StatusOK {
		t.Fatal("path traversal unexpectedly returned an asset")
	}
	csp := response.Header().Get("Content-Security-Policy")
	if csp == "" || strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "nonce-"+server.cspNonce) || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("security headers are missing")
	}
}

func TestStaticHTMLReceivesCSPNonce(t *testing.T) {
	server := testServer(t)
	server.assets = fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<html><script type="module">window.test = true</script></html>`), Mode: fs.FileMode(0o444)},
	}
	response := request(server, http.MethodGet, "/", nil, false, false)
	if response.Code != http.StatusOK {
		t.Fatalf("static HTML returned %d", response.Code)
	}
	expected := `<script nonce="` + server.cspNonce + `" type="module">`
	if !strings.Contains(response.Body.String(), expected) {
		t.Fatalf("static script did not receive CSP nonce: %s", response.Body.String())
	}
}

func TestStartReusesHealthyInstanceForSameClone(t *testing.T) {
	first := testServer(t)
	firstTarget, reused, err := first.Start()
	if err != nil {
		t.Fatal(err)
	}
	if reused {
		t.Fatal("first server unexpectedly reused another instance")
	}
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = first.Close(shutdown)
	})

	second, err := New(first.repository, 42, unavailableGitHub{}, ignoredRunner{})
	if err != nil {
		t.Fatal(err)
	}
	secondTarget, reused, err := second.Start()
	if err != nil {
		t.Fatal(err)
	}
	if !reused || secondTarget != firstTarget {
		t.Fatalf("second server did not reuse first instance: reused=%v target=%q want=%q", reused, secondTarget, firstTarget)
	}
}

func TestStartRecoversStaleInstanceRecord(t *testing.T) {
	server := testServer(t)
	directory := filepath.Dir(server.store.Path())
	if err := ensureLocalDirectory(directory); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(directory, "server.json")
	stale := `{"schema_version":1,"pid":999999,"origin":"http://127.0.0.1:1","start_url":"http://127.0.0.1:1/__passport/start/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","started_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(lockPath, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}

	target, reused, err := server.Start()
	if err != nil {
		t.Fatal(err)
	}
	if reused || !strings.HasPrefix(target, server.origin+"/__passport/start/") {
		t.Fatalf("stale instance was not replaced safely: reused=%v target=%q", reused, target)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Close(shutdown); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned instance record remained after shutdown: %v", err)
	}
}
