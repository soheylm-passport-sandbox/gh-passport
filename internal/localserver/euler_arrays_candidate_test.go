package localserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/missionverify"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/passportrepo"
)

// Never execute these scripts or connect to Euler; use the real local verifier.
func TestEulerArraysCandidateScript(t *testing.T) {
	raw, err := os.ReadFile("../missionverify/testdata/euler-arrays-candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	var packet struct {
		Cases []struct {
			ID      string  `json:"id"`
			Text    *string `json:"text"`
			Padding int     `json:"padding_bytes"`
			Passed  bool    `json:"local_pass"`
		} `json:"script_cases"`
	}
	if err := json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	for _, c := range packet.Cases {
		parent := t.TempDir()
		path := filepath.Join(parent, "practice", "workspace", "slurm", "array_job.slurm.txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if c.Text != nil {
			text := *c.Text
			if c.Padding > 0 {
				text += "#" + strings.Repeat("x", c.Padding-len(text)-1)
			}
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		server := &Server{repository: passportrepo.Repository{Root: filepath.Join(parent, ".transport")}}
		checks := server.verifySlurmArray()
		passed := true
		for _, ok := range checks {
			passed = passed && ok
		}
		line, _ := json.Marshal(map[string]any{"id": c.ID, "passed": passed, "checks": checks})
		fmt.Println("EULER_SCRIPT:" + string(line))
		if passed != c.Passed {
			t.Errorf("%s: %#v", c.ID, checks)
		}
	}
}

// Exercise the real authenticated loopback handler, catalogue loader, grader,
// receipt and retry state. GitHub and Euler are not contacted.
func TestEulerArraysCandidateHTTP(t *testing.T) {
	raw, err := os.ReadFile("../missionverify/testdata/euler-arrays-candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	var packet struct {
		Version string                `json:"curriculum_version"`
		Mission missionverify.Mission `json:"mission"`
		Cases   []struct {
			ID      string              `json:"id"`
			Answers map[string][]string `json:"answers"`
			Passed  bool                `json:"local_pass"`
			Invalid bool                `json:"invalid"`
		} `json:"cases"`
		Scripts []struct {
			ID      string  `json:"id"`
			Text    *string `json:"text"`
			Padding int     `json:"padding_bytes"`
			Passed  bool    `json:"local_pass"`
		} `json:"script_cases"`
	}
	if err := json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	server := testServer(t)
	server.repository.Passport.CurriculumVersion = packet.Version
	server.repository.Passport.Missions = []string{"euler-cpu-arrays"}
	if err := server.store.Save(server.defaultState()); err != nil {
		t.Fatal(err)
	}
	catalogue := missionverify.Catalog{SchemaVersion: 2, CurriculumVersion: packet.Version, Missions: map[string]missionverify.Mission{"euler-cpu-arrays": packet.Mission}}
	encoded, _ := json.Marshal(catalogue)
	if err := os.WriteFile(filepath.Join(server.repository.Root, "passport-curriculum.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(server.practiceRoot(), "workspace", "slurm", "array_job.slurm.txt")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	valid := *packet.Scripts[0].Text
	if err := os.WriteFile(file, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	verify := func(id string, answers map[string][]string, prefix string, passed, invalid bool) {
		body, _ := json.Marshal(missionverify.Attempt{Mission: "euler-cpu-arrays", Answers: answers, LocalInput: map[string]string{}, Attestation: missionverify.Attestation{Reviewed: true, NoSecrets: true, ObservedResult: true}})
		response := request(server, http.MethodPost, "/__passport/v2/verify", body, true, true)
		if invalid {
			if response.Code != http.StatusBadRequest {
				t.Fatalf("%s accepted invalid answers: %d", id, response.Code)
			}
			return
		}
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", id, response.Code, response.Body.String())
		}
		var result missionverify.Result
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		line, _ := json.Marshal(map[string]any{"id": id, "passed": result.Status == "ready_to_submit", "score": result.ScorePercent, "critical": result.CriticalPassed, "checks": result.Receipt["checks"]})
		fmt.Println(prefix + string(line))
		if (result.Status == "ready_to_submit") != passed {
			t.Fatalf("%s: %+v", id, result)
		}
	}
	for _, c := range packet.Cases {
		verify(c.ID, c.Answers, "EULER_HTTP_GRADE:", c.Passed, c.Invalid)
	}
	correct := map[string][]string{}
	for _, q := range packet.Mission.Activity.Questions {
		correct[q.ID] = q.Answer
	}
	for _, c := range packet.Scripts {
		if c.Text == nil {
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
		} else {
			text := *c.Text
			if c.Padding > 0 {
				text += "#" + strings.Repeat("x", c.Padding-len(text)-1)
			}
			if err := os.WriteFile(file, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
		}
		verify(c.ID, correct, "EULER_HTTP_SCRIPT:", c.Passed, false)
	}
	// A corrected retry after a file failure returns to the same valid result.
	if err := os.WriteFile(file, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	verify("restored_file_retry", correct, "EULER_HTTP_SCRIPT:", true, false)
	state, err := server.store.Load()
	if err != nil || state.CurriculumVersion != packet.Version || state.AttemptCounts["euler-cpu-arrays"] != len(packet.Cases)-1+len(packet.Scripts)+1 {
		t.Fatalf("retry state lost: %+v %v", state, err)
	}
}
