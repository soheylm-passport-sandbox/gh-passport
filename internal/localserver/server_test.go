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

type gitEnvironmentRunner struct {
	root  string
	name  string
	email string
}

type agentProjectRunner struct {
	changed string
}

func (runner agentProjectRunner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	if strings.Join(append([]string{name}, args...), " ") == "git diff --name-only HEAD -- workspace/agent_task" {
		return []byte(runner.changed), nil
	}
	return nil, errors.New("unexpected command")
}

func (runner gitEnvironmentRunner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	command := strings.Join(append([]string{name}, args...), " ")
	switch command {
	case "git --version":
		return []byte("git version 2.50.0\n"), nil
	case "gh auth status --hostname github.com":
		return []byte("Logged in\n"), nil
	case "git config --get user.name":
		return []byte(runner.name + "\n"), nil
	case "git config --get user.email":
		return []byte(runner.email + "\n"), nil
	case "git remote -v":
		return []byte("origin\thttps://github.com/student/passport-exercises.git (fetch)\n"), nil
	case "git branch --show-current":
		return []byte("practice/student\n"), nil
	case "git rev-parse --show-toplevel":
		return []byte(runner.root + "\n"), nil
	default:
		return nil, errors.New("unexpected command: " + command)
	}
}

func TestGitEnvironmentUsesPracticeAndRejectsManagedTransportIdentity(t *testing.T) {
	parent := t.TempDir()
	transport := filepath.Join(parent, ".transport")
	practice := filepath.Join(parent, "practice")
	server := &Server{repository: passportrepo.Repository{
		Root:     transport,
		Passport: passportrepo.Passport{GitHubUser: "student"},
	}}
	server.runner = gitEnvironmentRunner{root: practice, name: "Student Name", email: "student@example.org"}
	checks := server.verifyGitEnvironment()
	for name, passed := range checks {
		if !passed {
			t.Fatalf("learner practice check %s failed: %#v", name, checks)
		}
	}
	server.runner = gitEnvironmentRunner{root: practice, name: passportrepo.ManagedGitName, email: passportrepo.ManagedGitEmail}
	checks = server.verifyGitEnvironment()
	if checks["identity_name"] || checks["identity_email"] {
		t.Fatalf("managed transport identity satisfied the Git mission: %#v", checks)
	}
}

func TestAgentVerifierEnforcesBoundedDiffAndCanary(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "practice", "workspace", "agent_task")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	plan := "P: is durable. D: is temporary. C: is not project storage. Heavy work runs on Euler.\n"
	if err := os.WriteFile(filepath.Join(root, "storage-plan.md"), []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scope-canary.txt"), []byte("IDEAL-PASSPORT-AGENT-SCOPE-CANARY-v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{repository: passportrepo.Repository{Root: filepath.Join(parent, ".transport")}}
	server.runner = agentProjectRunner{changed: "workspace/agent_task/storage-plan.md\n"}
	checks := server.verifyAgentProject()
	for name, passed := range checks {
		if !passed {
			t.Fatalf("safe agent project check %s failed: %#v", name, checks)
		}
	}
	server.runner = agentProjectRunner{changed: "workspace/agent_task/storage-plan.md\nworkspace/agent_task/scope-canary.txt\n"}
	checks = server.verifyAgentProject()
	if checks["bounded_diff"] {
		t.Fatalf("extra changed file passed bounded diff: %#v", checks)
	}
	if err := os.WriteFile(filepath.Join(root, "scope-canary.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checks = server.verifyAgentProject()
	if checks["canary_unchanged"] {
		t.Fatalf("changed canary passed: %#v", checks)
	}
}

func TestSubmissionRetryAcceptsOnlyItsOwnInterruptedGeneratedFiles(t *testing.T) {
	allowed := map[string]bool{
		"submissions/core-orientation.json": true,
		"workspace/task/answer.txt":         true,
	}
	for _, status := range []string{
		"?? submissions/core-orientation.json\n",
		"A  submissions/core-orientation.json\n M workspace/task/answer.txt\n",
		"MM submissions/core-orientation.json\n",
	} {
		if !onlyManagedSubmissionChanges(status, allowed) {
			t.Fatalf("safe interrupted submission was rejected: %q", status)
		}
	}
	for _, status := range []string{
		"",
		"?? notes.txt\n",
		"A  submissions/core-orientation.json\n?? secret.txt\n",
		"R  submissions/core-orientation.json -> stolen.json\n",
	} {
		if onlyManagedSubmissionChanges(status, allowed) {
			t.Fatalf("unexpected transport change was accepted: %q", status)
		}
	}
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

func TestFutureMissionCannotBeVerifiedBeforeCurrentMission(t *testing.T) {
	server := testServer(t)
	server.repository.Passport.Missions = append(
		server.repository.Passport.Missions,
		"core-accounts-secrets",
	)
	body := []byte(`{"mission":"core-accounts-secrets","answers":{},"local_input":{},"attestation":{"reviewed":true,"no_secrets":true,"observed_result":true}}`)
	response := request(server, http.MethodPost, "/__passport/v2/verify", body, true, true)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "mission_not_current") {
		t.Fatalf("future mission returned %d: %s", response.Code, response.Body.String())
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

func TestReplaceRegularFileReplacesExistingContent(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "submission.json")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	temporary, err := os.CreateTemp(directory, ".new-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := temporary.WriteString("new"); err != nil {
		t.Fatal(err)
	}
	if err := temporary.Close(); err != nil {
		t.Fatal(err)
	}
	if err := replaceRegularFile(temporary.Name(), target); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "new" {
		t.Fatalf("managed file was not replaced: content=%q err=%v", content, err)
	}
	matches, err := filepath.Glob(filepath.Join(directory, ".passport-backup-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("managed backup remained after success: %v %v", matches, err)
	}
}

func TestManagedFileBoundariesRejectTraversalAndSymlinks(t *testing.T) {
	root := t.TempDir()
	if _, err := boundedPath(root, "../outside"); err == nil {
		t.Fatal("path traversal was accepted")
	}
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links unavailable on this platform: %v", err)
	}
	if _, err := boundedRegularFile(root, "link.txt", 1024); err == nil {
		t.Fatal("symbolic-link artifact was accepted")
	}
}

func TestCopyMissionArtifactsCopiesOnlyDeclaredRegularFile(t *testing.T) {
	parent := t.TempDir()
	transport := filepath.Join(parent, ".transport")
	practice := filepath.Join(parent, "practice")
	for _, root := range []string{transport, practice} {
		if err := os.MkdirAll(filepath.Join(root, "workspace", "task"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	declared := filepath.Join(practice, "workspace", "task", "answer.txt")
	undeclared := filepath.Join(practice, "workspace", "task", "secret.txt")
	if err := os.WriteFile(declared, []byte("synthetic answer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(undeclared, []byte("must not copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{repository: passportrepo.Repository{Root: transport}}
	if err := server.copyMissionArtifacts(transport, []string{"workspace/task/answer.txt"}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(transport, "workspace", "task", "answer.txt"))
	if err != nil || string(content) != "synthetic answer" {
		t.Fatalf("declared artifact was not copied: content=%q err=%v", content, err)
	}
	if _, err := os.Stat(filepath.Join(transport, "workspace", "task", "secret.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("undeclared artifact was copied: %v", err)
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
