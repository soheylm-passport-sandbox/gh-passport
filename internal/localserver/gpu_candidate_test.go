package localserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/localstate"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/missionverify"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/passportrepo"
)

type gpuCandidatePacket struct {
	Questions []missionverify.Question
	Answers   []struct {
		ID      string
		Answers map[string][]string
		Passed  bool `json:"local_pass"`
	} `json:"answer_cases"`
	Scripts []struct {
		ID      string
		Text    *string
		Padding int  `json:"padding_bytes"`
		Passed  bool `json:"local_pass"`
	} `json:"script_cases"`
}

func gpuCandidate(t *testing.T) (gpuCandidatePacket, missionverify.Mission) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	raw, err := os.ReadFile(filepath.Join(root, "agent_wiki/assessment_candidates/gpu_resources/packet.json"))
	if os.IsNotExist(err) {
		t.Skip("inactive GPU candidate not exported")
	}
	if err != nil {
		t.Fatal(err)
	}
	var packet gpuCandidatePacket
	if err := json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(root, "curriculum/compatibility/7.0.0.json"))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Missions map[string]struct{ Metadata missionverify.Mission }
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	mission := snapshot.Missions["euler-gpu-review"].Metadata
	if strings.Contains(t.Name(), "Versioned") {
		raw, err = os.ReadFile(filepath.Join(root, "curriculum/proposals/8.0.0.json"))
		if err != nil {
			t.Fatal(err)
		}
		var snapshot struct {
			Missions map[string]struct{ Metadata missionverify.Mission }
		}
		if err = json.Unmarshal(raw, &snapshot); err != nil {
			t.Fatal(err)
		}
		mission = snapshot.Missions["euler-gpu-review"].Metadata
	}
	mission.Activity.Questions = packet.Questions
	return packet, mission
}

// Call actual production graders; never execute a Slurm script or contact Euler.
func TestGPUCandidateGraders(t *testing.T) {
	packet, mission := gpuCandidate(t)
	catalog := missionverify.Catalog{Missions: map[string]missionverify.Mission{"euler-gpu-review": mission}}
	for _, c := range packet.Answers {
		_, result, err := missionverify.Grade(catalog, missionverify.Attempt{Mission: "euler-gpu-review", Answers: c.Answers})
		passed := err == nil && result.Status == "ready_to_submit"
		line, _ := json.Marshal(map[string]any{"id": c.ID, "passed": passed, "score": result.ScorePercent, "critical": result.CriticalPassed, "invalid": err != nil, "feedback": result.Feedback})
		fmt.Println("GPU_GRADE:" + string(line))
		if passed != c.Passed {
			t.Errorf("%s: %+v %v", c.ID, result, err)
		}
	}
	for _, c := range packet.Scripts {
		parent := t.TempDir()
		path := filepath.Join(parent, "practice/workspace/slurm/gpu_job.slurm.txt")
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
		}
		server := &Server{repository: passportrepo.Repository{Root: filepath.Join(parent, ".transport")}}
		checks := server.verifyGPUScript()
		passed := true
		for _, ok := range checks {
			passed = passed && ok
		}
		line, _ := json.Marshal(map[string]any{"id": c.ID, "passed": passed, "checks": checks})
		fmt.Println("GPU_SCRIPT:" + string(line))
		if passed != c.Passed {
			t.Errorf("%s: %#v", c.ID, checks)
		}
	}
}

func TestGPUCandidateHTTP(t *testing.T) { testGPUHTTP(t, "7.0.0") }

func TestGPUVersionedHTTP(t *testing.T) { testGPUHTTP(t, "8.0.0") }

func testGPUHTTP(t *testing.T, version string) {
	packet, mission := gpuCandidate(t)
	server := testServer(t)
	server.repository.Passport.CurriculumVersion = version
	server.repository.Passport.Missions = []string{"euler-gpu-review"}
	if err := server.store.Save(server.defaultState()); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(missionverify.Catalog{SchemaVersion: 2, CurriculumVersion: version, Missions: map[string]missionverify.Mission{"euler-gpu-review": mission}})
	if err := os.WriteFile(filepath.Join(server.repository.Root, "passport-curriculum.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(server.practiceRoot(), "workspace/slurm/gpu_job.slurm.txt")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(*packet.Scripts[0].Text), 0600); err != nil {
		t.Fatal(err)
	}
	for _, c := range packet.Answers {
		body, _ := json.Marshal(missionverify.Attempt{Mission: "euler-gpu-review", Answers: c.Answers, LocalInput: map[string]string{}, Attestation: missionverify.Attestation{Reviewed: true, NoSecrets: true, ObservedResult: true}})
		response := request(server, http.MethodPost, "/__passport/v2/verify", body, true, true)
		if c.ID == "unknown_field" {
			if response.Code != http.StatusBadRequest {
				t.Fatalf("unknown accepted: %d", response.Code)
			}
			continue
		}
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", c.ID, response.Code, response.Body.String())
		}
		var result missionverify.Result
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if (result.Status == "ready_to_submit") != c.Passed {
			t.Fatalf("%s: %+v", c.ID, result)
		}
	}
	answers := map[string][]string{}
	for _, q := range packet.Questions {
		answers[q.ID] = q.Answer
	}
	body, _ := json.Marshal(missionverify.Attempt{Mission: "euler-gpu-review", Answers: answers, LocalInput: map[string]string{}, Attestation: missionverify.Attestation{Reviewed: true, NoSecrets: true, ObservedResult: true}})
	verifyRepair := func(want string) {
		response := request(server, http.MethodPost, "/__passport/v2/verify", body, true, true)
		var result missionverify.Result
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Status != want {
			t.Fatalf("repair: %d %s", response.Code, response.Body.String())
		}
	}
	verifyRepair("ready_to_submit")
	if version == "8.0.0" {
		response := request(server, http.MethodPost, "/__passport/v2/verify", body, true, true)
		var result missionverify.Result
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatal(response.Body.String())
		}
		if len(result.Receipt) != 3 {
			t.Fatalf("unexpected submission receipt fields: %+v", result.Receipt)
		}
		record, _ := json.Marshal(result.Receipt)
		fmt.Println("GPU_VERSIONED_RECEIPT:" + string(record))
	}
	valid := *packet.Scripts[0].Text
	if err := os.WriteFile(file, []byte(strings.ReplaceAll(valid, "rtx_4090:1", "rtx_4090:2")), 0600); err != nil {
		t.Fatal(err)
	}
	verifyRepair("needs_work")
	if err := os.WriteFile(file, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	verifyRepair("ready_to_submit")
	state, err := server.store.Load()
	if err != nil || state.AttemptCounts["euler-gpu-review"] != len(packet.Answers)+2+func() int {
		if version == "8.0.0" {
			return 1
		}
		return 0
	}() {
		t.Fatalf("attempt accounting: %+v %v", state, err)
	}
	// A pending submission stays read-only even with all correct candidate answers.
	state.PendingSubmissions = map[string]localstate.PendingSubmission{"euler-gpu-review": {HeadSHA: strings.Repeat("a", 40), SubmittedAt: "2026-10-13T10:00:00Z"}}
	if err := server.store.Save(state); err != nil {
		t.Fatal(err)
	}
	if response := request(server, http.MethodPost, "/__passport/v2/verify", body, true, true); response.Code != http.StatusConflict {
		t.Fatalf("pending unlocked: %d", response.Code)
	}
	after, err := server.store.Load()
	if err != nil || after.AttemptCounts["euler-gpu-review"] != state.AttemptCounts["euler-gpu-review"] || after.PendingSubmissions["euler-gpu-review"].HeadSHA != strings.Repeat("a", 40) {
		t.Fatalf("pending state changed: %+v %v", after, err)
	}
	fmt.Println("GPU_HTTP: retry/counters/pending passed")
	if version == "8.0.0" {
		fmt.Println("GPU_VERSIONED_HTTP: retry/counters/pending passed")
	}
}
