package localserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/githubstatus"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/handover"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/missionverify"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/passportrepo"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type handoverPacket struct {
	Template string
	Day      string
	Mission  missionverify.Mission
	Cases    []struct {
		ID        string
		RawBase64 string `json:"raw_base64"`
		Pass      bool
		Messages  []string
	}
	Answers []struct {
		ID       string
		Answers  map[string][]string
		Pass     bool
		Score    int
		Critical bool
	} `json:"answer_cases"`
}

func loadHandoverPacket(t *testing.T) handoverPacket {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../handover/testdata/qualification.json"))
	if os.IsNotExist(err) {
		t.Skip("local qualification packet unavailable in exported release")
	}
	if err != nil {
		t.Fatal(err)
	}
	var p handoverPacket
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestHandoverV2Concordance(t *testing.T) {
	p := loadHandoverPacket(t)
	day, _ := time.Parse("2006-01-02", p.Day)
	if handover.Template() != p.Template {
		t.Fatal("Go/Python worksheet differs")
	}
	for _, c := range p.Cases {
		t.Run(c.ID, func(t *testing.T) {
			raw, err := base64.StdEncoding.DecodeString(c.RawBase64)
			if err != nil {
				t.Fatal(err)
			}
			messages := handover.Assess(raw, handover.Contract.Revision, day)
			actual, _ := json.Marshal(messages)
			expected, _ := json.Marshal(c.Messages)
			if string(actual) != string(expected) {
				t.Fatalf("messages=%s expected=%s", actual, expected)
			}
			s := testServer(t)
			target := filepath.Join(s.practiceRoot(), "workspace/handover/project-handover.md")
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(target, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err = handover.Prepare(context.Background(), s.handoverRoot()); err != nil {
				t.Fatal(err)
			}
			receipt, err := s.localReceipt(p.Mission, nil)
			if err != nil {
				t.Fatal(err)
			}
			if receipt["passed"] != c.Pass {
				t.Fatalf("receipt=%#v", receipt)
			}
			after, _ := os.ReadFile(target)
			if string(after) != string(raw) {
				t.Fatal("document changed")
			}
			fmt.Println("HANDOVER_V2_DOC:" + c.ID)
		})
	}
	catalog := missionverify.Catalog{Missions: map[string]missionverify.Mission{"data-handover": p.Mission}}
	for _, c := range p.Answers {
		_, result, err := missionverify.Grade(catalog, missionverify.Attempt{Mission: "data-handover", Answers: c.Answers})
		if (err == nil && result.Status == "ready_to_submit") != c.Pass || result.ScorePercent != c.Score || result.CriticalPassed != c.Critical {
			t.Fatalf("%s: %v %#v", c.ID, err, result)
		}
		fmt.Println("HANDOVER_V2_ANSWER:" + c.ID)
	}
}
func handoverServer(t *testing.T) (*Server, *practiceRunner) {
	t.Helper()
	s := testServer(t)
	s.repository.Passport.CurriculumVersion = "6.0.0"
	s.repository.Passport.Missions = []string{"data-handover"}
	p := loadHandoverPacket(t)
	catalog := missionverify.Catalog{SchemaVersion: 2, CurriculumVersion: "6.0.0", Missions: map[string]missionverify.Mission{"data-handover": p.Mission}}
	raw, _ := json.Marshal(catalog)
	if err := os.WriteFile(filepath.Join(s.repository.Root, "passport-curriculum.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	runner := &practiceRunner{root: s.practiceRoot(), origin: "https://github.com/student/passport-exercises.git"}
	s.runner = runner
	return s, runner
}
func TestHandoverPreparationAPI(t *testing.T) {
	s, _ := handoverServer(t)
	for _, auth := range []struct {
		session, origin bool
		code            int
	}{{false, true, 401}, {true, false, 403}} {
		res := request(s, http.MethodPost, "/__passport/v2/handover", nil, auth.session, auth.origin)
		if res.Code != auth.code {
			t.Fatal(res.Code)
		}
	}
	res := request(s, http.MethodPost, "/__passport/v2/handover", nil, true, true)
	if res.Code != 200 {
		t.Fatalf("%d %s", res.Code, res.Body.String())
	}
	var result struct {
		Handover struct {
			ProjectPath string `json:"project_path"`
			ReprisePath string `json:"reprise_path"`
			Revision    string
		}
	}
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Handover.ProjectPath != s.handoverRoot() || result.Handover.Revision != handover.Contract.Revision || filepath.Dir(result.Handover.ReprisePath) != filepath.Dir(s.handoverRoot()) {
		t.Fatal(result)
	}
	worksheet := filepath.Join(s.practiceRoot(), "workspace/handover/project-handover.md")
	rawWorksheet, err := os.ReadFile(worksheet)
	if err != nil || string(rawWorksheet) != handover.Template() {
		t.Fatal("missing new worksheet")
	}
	os.WriteFile(worksheet, []byte("# synthetic learner draft"), 0600)
	if res = request(s, http.MethodPost, "/__passport/v2/handover", nil, true, true); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	kept, _ := os.ReadFile(worksheet)
	if string(kept) != "# synthetic learner draft" {
		t.Fatal("worksheet draft overwritten")
	}
	target := filepath.Join(s.handoverRoot(), "verify.py")
	os.WriteFile(target, []byte("# synthetic changed work"), 0600)
	if res = request(s, http.MethodPost, "/__passport/v2/handover", nil, true, true); res.Code != 409 {
		t.Fatal(res.Body.String())
	}
	raw, _ := os.ReadFile(target)
	if string(raw) != "# synthetic changed work" {
		t.Fatal("modified work overwritten")
	}
}
func TestHandoverPreparationGuardsKeepOldAndPendingState(t *testing.T) {
	for _, v := range []string{"2.1.2", "3.0.0", "4.0.0", "5.0.0", "6.0.0-pending", "6.0.0-other"} {
		t.Run(v, func(t *testing.T) {
			s, runner := handoverServer(t)
			switch v {
			case "6.0.0-pending":
				s.repository.RemoteHeadSHA = s.repository.HeadSHA
				s.markPendingSubmission("data-handover")
			case "6.0.0-other":
				s.repository.Passport.Missions = []string{"core-orientation"}
			default:
				s.repository.Passport.CurriculumVersion = v
			}
			before, _ := s.store.Load()
			res := request(s, http.MethodPost, "/__passport/v2/handover", nil, true, true)
			if res.Code != 409 || len(runner.calls) != 0 {
				t.Fatalf("%s %s calls=%v", v, res.Body.String(), runner.calls)
			}
			after, _ := s.store.Load()
			beforeRaw, _ := json.Marshal(before)
			afterRaw, _ := json.Marshal(after)
			if string(afterRaw) != string(beforeRaw) {
				t.Fatal("state changed")
			}
			if _, err := os.Lstat(s.handoverRoot()); !os.IsNotExist(err) {
				t.Fatal("guard created project")
			}
		})
	}
}

func TestHandoverWorksheetAPIRefusesNonregularTargets(t *testing.T) {
	for _, kind := range []string{"directory", "linked"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := handoverServer(t)
			parent := filepath.Join(s.practiceRoot(), "workspace/handover")
			if err := os.MkdirAll(parent, 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(parent, "project-handover.md")
			outside := filepath.Join(s.repository.Root, "synthetic-canary.md")
			os.WriteFile(outside, []byte("synthetic canary"), 0600)
			if kind == "directory" {
				os.Mkdir(target, 0700)
			} else if err := os.Symlink(outside, target); err != nil {
				t.Skip("symbolic links unavailable")
			}
			res := request(s, http.MethodPost, "/__passport/v2/handover", nil, true, true)
			if res.Code != 409 {
				t.Fatal(res.Body.String())
			}
			var value map[string]string
			json.Unmarshal(res.Body.Bytes(), &value)
			if value["error"] != "handover_worksheet_not_safe" {
				t.Fatal(value)
			}
			kept, _ := os.ReadFile(outside)
			if string(kept) != "synthetic canary" {
				t.Fatal("linked work changed")
			}
		})
	}
}

func TestHandoverLocalBundleServedByRealServer(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	if _, err := os.Stat(filepath.Join(filepath.Dir(file), "../webdist/bundle/missions/data-handover/index.html")); err != nil {
		t.Skip("release-shaped bundle is tested after packaging")
	}
	original, _ := handoverServer(t)
	cmd := exec.Command("git", "init", "--quiet", "--template="+t.TempDir(), original.repository.Root)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthetic transport: %v %s", err, raw)
	}
	if err := os.WriteFile(filepath.Join(original.repository.Root, ".gitignore"), []byte(".passport-local/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server, err := New(original.repository, 42, unavailableGitHub{}, passportrepo.ExecRunner{})
	if err != nil {
		t.Fatal(err)
	}
	server.origin = original.origin
	response := request(server, http.MethodGet, "/missions/data-handover/", nil, true, false)
	if response.Code != 200 {
		t.Fatalf("native page unavailable: %d", response.Code)
	}
	html := response.Body.String()
	for _, value := range []string{"handover_packet_v2", "recover_inputs", "verify_result", "retain_input", "/__passport/v2/handover"} {
		if !strings.Contains(html, value) {
			t.Fatalf("missing native proposal content: %s", value)
		}
	}
}

func TestHandoverComputerPreparationPreservesCompletedProgress(t *testing.T) {
	server, _ := handoverServer(t)
	server.official = &githubstatus.Official{Status: githubstatus.ControllerStatus{HeadSHA: server.repository.HeadSHA, Stage: "complete"}}
	state := server.defaultState()
	state.MissionDrafts = map[string]map[string][]string{"data-handover": {"recover_inputs": {"approved_copy"}}}
	if err := server.store.Save(state); err != nil {
		t.Fatal(err)
	}
	before, _ := server.store.Load()
	beforeRaw, _ := json.Marshal(before)
	if res := request(server, http.MethodPost, "/__passport/v2/handover", nil, true, true); res.Code != 409 {
		t.Fatal("completed assessment mutation allowed")
	}
	if res := request(server, http.MethodPost, "/__passport/v2/handover?setup=computer", nil, true, true); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	after, _ := server.store.Load()
	afterRaw, _ := json.Marshal(after)
	if string(beforeRaw) != string(afterRaw) {
		t.Fatal("computer preparation changed state")
	}
	res := request(server, http.MethodPost, "/__passport/v2/computer-check", []byte(`{"mission":"data-handover"}`), true, true)
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	var result struct {
		AssessmentChanged bool `json:"assessment_changed"`
		Passed            bool
		Checks            map[string]bool
	}
	json.Unmarshal(res.Body.Bytes(), &result)
	if result.AssessmentChanged || !result.Passed || !result.Checks["fixture_inventory"] {
		t.Fatal(result)
	}
	if server.official.Status.Stage != "complete" {
		t.Fatal("completion changed")
	}
	server.repository.HeadSHA = strings.Repeat("b", 40)
	server.repository.RemoteHeadSHA = server.repository.HeadSHA
	server.markPendingSubmission("data-handover")
	if res := request(server, http.MethodPost, "/__passport/v2/handover?setup=computer", nil, true, true); res.Code != 409 {
		t.Fatal("pending preparation allowed")
	}
	server, _ = handoverServer(t)
	server.repository.Passport.Missions = []string{"core-orientation"}
	if res := request(server, http.MethodPost, "/__passport/v2/handover?setup=computer", nil, true, true); res.Code != 409 {
		t.Fatal("unassigned preparation allowed")
	}
}
