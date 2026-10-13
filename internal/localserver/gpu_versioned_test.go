package localserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/githubstatus"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/gpureview"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/localstate"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/missionverify"
)

func TestGPUVersionedScripts(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	raw, err := os.ReadFile(filepath.Join(root, "agent_wiki/assessment_candidates/gpu_resources/versioned_cases.json"))
	if os.IsNotExist(err) {
		t.Skip("inactive GPU proposal not exported")
	}
	if err != nil {
		t.Fatal(err)
	}
	var packet struct {
		Scripts []struct {
			ID      string
			Text    *string
			Padding int `json:"padding_bytes"`
			Passed  bool
		} `json:"script_cases"`
	}
	if err = json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	for _, c := range packet.Scripts {
		t.Run(c.ID, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, gpureview.Contract.Path)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if c.Text != nil {
				text := *c.Text
				if c.Padding > 0 {
					text += "#" + strings.Repeat("x", c.Padding-len(text)-1)
				}
				if err := os.WriteFile(path, []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			} else if c.ID == "invalid_utf8" {
				if err := os.WriteFile(path, []byte{0xff}, 0600); err != nil {
					t.Fatal(err)
				}
			}
			checks := gpureview.CheckFile(root)
			passed := gpureview.Feedback(checks) == ""
			record, _ := json.Marshal(map[string]any{"id": c.ID, "checks": checks, "passed": passed, "feedback": gpureview.Feedback(checks)})
			fmt.Println("GPU_VERSIONED_SCRIPT:" + string(record))
			if passed != c.Passed {
				t.Fatalf("%s: %+v", c.ID, checks)
			}
		})
	}
}

func TestGPUVersionedRefusesLinkedAndNonregularFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, gpureview.Contract.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if gpureview.CheckFile(root)["bounded_file"] {
		t.Fatal("directory accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "fixture.txt")
	if err := os.WriteFile(target, []byte("fictional"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skip("symlinks unavailable")
	}
	if gpureview.CheckFile(root)["bounded_file"] {
		t.Fatal("linked file accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(target), filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if gpureview.CheckFile(root)["bounded_file"] {
		t.Fatal("linked parent accepted")
	}
}

// Loading the new proposal must not replace any old draft or completion meaning.
func TestGPUVersionedPreservesHistoricalState(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	if _, err := os.Stat(filepath.Join(root, "curriculum/compatibility/7.0.0.json")); os.IsNotExist(err) {
		t.Skip("proposal not exported")
	}
	for _, version := range []string{"2.1.2", "3.0.0", "4.0.0", "5.0.0", "6.0.0", "7.0.0"} {
		t.Run(version, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, "curriculum/compatibility", version+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var snapshot struct {
				Missions map[string]struct{ Metadata missionverify.Mission }
			}
			if err = json.Unmarshal(raw, &snapshot); err != nil {
				t.Fatal(err)
			}
			mission := snapshot.Missions["euler-gpu-review"].Metadata
			if len(mission.Activity.Questions) != 2 || mission.Verification.LocalVerifier != "gpu_script" {
				t.Fatal("old meaning changed")
			}
			server := testServer(t)
			server.repository.Passport.CurriculumVersion = version
			server.repository.Passport.Missions = []string{"euler-gpu-review"}
			catalog, _ := json.Marshal(missionverify.Catalog{SchemaVersion: 2, CurriculumVersion: version, Missions: map[string]missionverify.Mission{"euler-gpu-review": mission}})
			if err = os.WriteFile(filepath.Join(server.repository.Root, "passport-curriculum.json"), catalog, 0600); err != nil {
				t.Fatal(err)
			}
			state := server.defaultState()
			draft := map[string][]string{}
			for _, q := range mission.Activity.Questions {
				draft[q.ID] = q.Answer
			}
			state.MissionDrafts = map[string]map[string][]string{"euler-gpu-review": draft}
			state.AttemptCounts = map[string]int{"euler-gpu-review": 9}
			state.ExpandedHelp = []string{"gpu-model"}
			state.LaunchCount = 3
			state.LastOpenedMission = "euler-gpu-review"
			state.PendingSubmissions = map[string]localstate.PendingSubmission{"euler-gpu-review": {HeadSHA: strings.Repeat("a", 40), SubmittedAt: "2026-10-13T10:00:00Z"}}
			if err = server.store.Save(state); err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(missionverify.Attempt{Mission: "euler-gpu-review", Answers: draft})
			before, _ := os.ReadFile(server.store.Path())
			response := request(server, http.MethodPost, "/__passport/v2/verify", body, true, true)
			after, _ := os.ReadFile(server.store.Path())
			if response.Code != http.StatusConflict || !bytes.Equal(before, after) {
				t.Fatal("pending old state changed")
			}
			// A completion reported by the trusted controller stays read-only on reopen.
			state.PendingSubmissions = nil
			if err = server.store.Save(state); err != nil {
				t.Fatal(err)
			}
			server.official = &githubstatus.Official{Status: githubstatus.ControllerStatus{HeadSHA: server.repository.HeadSHA}}
			before, _ = os.ReadFile(server.store.Path())
			response = request(server, http.MethodPost, "/__passport/v2/verify", body, true, true)
			after, _ = os.ReadFile(server.store.Path())
			reopened, err := localstate.New(server.repository.Root).Load()
			if response.Code != http.StatusConflict || !bytes.Equal(before, after) || err != nil || reopened.PassportID != state.PassportID || reopened.RouteDigest != state.RouteDigest || reopened.CurriculumVersion != version || reopened.AttemptCounts["euler-gpu-review"] != 9 {
				t.Fatal("completed old state changed")
			}
		})
	}
	fmt.Println("GPU_VERSIONED_STATE: six historical draft/pending/completed reopens preserved")
}
