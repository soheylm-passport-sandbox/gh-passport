package localserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/githubstatus"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/localstate"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/missionverify"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/slurmreview"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSlurm9HTTP(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	raw, err := os.ReadFile(filepath.Join(root, "tests/fixtures/slurm_review_cases.json"))
	if os.IsNotExist(err) {
		t.Skip("source fixtures")
	}
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ ID, Profile, Text string }
	json.Unmarshal(raw, &cases)
	for _, profile := range []string{"array", "gpu"} {
		t.Run(profile, func(t *testing.T) {
			ident := "euler-cpu-arrays"
			if profile == "gpu" {
				ident = "euler-gpu-review"
			}
			raw, err := os.ReadFile(filepath.Join(root, "curriculum/missions", ident, "mission.json"))
			if err != nil {
				t.Fatal(err)
			}
			var m missionverify.Mission
			json.Unmarshal(raw, &m)
			server := testServer(t)
			server.repository.Passport.CurriculumVersion = "9.0.0"
			server.repository.Passport.Missions = []string{ident}
			server.store.Save(server.defaultState())
			catalog, _ := json.Marshal(missionverify.Catalog{SchemaVersion: 2, CurriculumVersion: "9.0.0", Missions: map[string]missionverify.Mission{ident: m}})
			os.WriteFile(filepath.Join(server.repository.Root, "passport-curriculum.json"), catalog, 0600)
			path := filepath.Join(server.practiceRoot(), slurmreview.Contract.Profiles[profile].Path)
			os.MkdirAll(filepath.Dir(path), 0700)
			answers := map[string][]string{}
			for _, q := range m.Activity.Questions {
				answers[q.ID] = q.Answer
			}
			body, _ := json.Marshal(missionverify.Attempt{Mission: ident, Answers: answers, LocalInput: map[string]string{}, Attestation: missionverify.Attestation{Reviewed: true, NoSecrets: true, ObservedResult: true}})
			valid := ""
			for _, c := range cases {
				if c.ID == profile+"-valid" {
					valid = c.Text
				}
			}
			verify := func(want string) missionverify.Result {
				r := request(server, http.MethodPost, "/__passport/v2/verify", body, true, true)
				var result missionverify.Result
				if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || result.Status != want {
					t.Fatalf("%d %s", r.Code, r.Body.String())
				}
				return result
			}
			os.WriteFile(path, []byte(valid+"#SBATCH --nodes=4\n"), 0600)
			bad := verify("needs_work")
			if !strings.Contains(fmt.Sprint(bad.Receipt), "one node") {
				t.Fatalf("missing useful feedback: %+v", bad)
			}
			os.WriteFile(path, []byte(valid), 0600)
			good := verify("ready_to_submit")
			if len(good.Receipt) != 3 {
				t.Fatalf("receipt has extra fields: %+v", good.Receipt)
			}
			rec, _ := json.Marshal(map[string]any{"profile": profile, "receipt": good.Receipt})
			fmt.Println("SLURM_RECEIPT:" + string(rec))
			state, _ := server.store.Load()
			state.PendingSubmissions = map[string]localstate.PendingSubmission{ident: {HeadSHA: strings.Repeat("a", 40), SubmittedAt: "2026-10-13T12:00:00Z"}}
			server.store.Save(state)
			before, _ := os.ReadFile(server.store.Path())
			r := request(server, http.MethodPost, "/__passport/v2/verify", body, true, true)
			if r.Code != 409 {
				t.Fatal("pending not protected", r.Code)
			}
			after, _ := server.store.Load()
			if after.AttemptCounts[ident] != state.AttemptCounts[ident] || after.PendingSubmissions[ident] != state.PendingSubmissions[ident] {
				t.Fatal("pending changed")
			}
			afterBytes, _ := os.ReadFile(server.store.Path())
			if !bytes.Equal(before, afterBytes) {
				t.Fatal("pending bytes changed")
			}
		})
	}
}

// Existing drafts, attempts, pending and complete states remain byte-identical.
func TestSlurm9PreservesHistoricalState(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	if _, err := os.Stat(filepath.Join(root, "curriculum/compatibility/8.0.0.json")); os.IsNotExist(err) {
		t.Skip("source compatibility fixtures")
	}
	for _, version := range []string{"2.1.2", "3.0.0", "4.0.0", "5.0.0", "6.0.0", "7.0.0", "8.0.0"} {
		for _, ident := range []string{"euler-cpu-arrays", "euler-gpu-review"} {
			t.Run(version+ident, func(t *testing.T) {
				raw, err := os.ReadFile(filepath.Join(root, "curriculum/compatibility", version+".json"))
				if err != nil {
					t.Fatal(err)
				}
				var snapshot struct {
					Missions map[string]struct{ Metadata missionverify.Mission }
				}
				json.Unmarshal(raw, &snapshot)
				m := snapshot.Missions[ident].Metadata
				if strings.HasSuffix(m.Verification.LocalVerifier, "_v3") {
					t.Fatal("historical check changed")
				}
				s := testServer(t)
				s.repository.Passport.CurriculumVersion = version
				s.repository.Passport.Missions = []string{ident}
				catalog, _ := json.Marshal(missionverify.Catalog{SchemaVersion: 2, CurriculumVersion: version, Missions: map[string]missionverify.Mission{ident: m}})
				os.WriteFile(filepath.Join(s.repository.Root, "passport-curriculum.json"), catalog, 0600)
				state := s.defaultState()
				draft := map[string][]string{}
				for _, q := range m.Activity.Questions {
					draft[q.ID] = q.Answer
				}
				state.MissionDrafts = map[string]map[string][]string{ident: draft}
				state.AttemptCounts = map[string]int{ident: 9}
				state.ExpandedHelp = []string{"synthetic-help"}
				state.LastOpenedMission = ident
				state.PendingSubmissions = map[string]localstate.PendingSubmission{ident: {HeadSHA: strings.Repeat("a", 40), SubmittedAt: "2026-10-13T12:00:00Z"}}
				body, _ := json.Marshal(missionverify.Attempt{Mission: ident, Answers: draft})
				check := func() {
					before, _ := os.ReadFile(s.store.Path())
					r := request(s, http.MethodPost, "/__passport/v2/verify", body, true, true)
					after, _ := os.ReadFile(s.store.Path())
					reopened, err := localstate.New(s.repository.Root).Load()
					if r.Code != 409 || !bytes.Equal(before, after) || err != nil || reopened.CurriculumVersion != version || reopened.RouteDigest != state.RouteDigest || reopened.AttemptCounts[ident] != 9 {
						t.Fatal("historical state modified")
					}
				}
				s.store.Save(state)
				check()
				state.PendingSubmissions = nil
				s.store.Save(state)
				s.official = &githubstatus.Official{Status: githubstatus.ControllerStatus{HeadSHA: s.repository.HeadSHA}}
				check()
			})
		}
	}
}
