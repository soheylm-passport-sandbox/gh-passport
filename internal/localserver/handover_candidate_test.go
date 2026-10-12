package localserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/missionverify"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/passportrepo"
)

// Diagnostic only: do not strengthen an installed handover receipt silently.
func TestHandoverCandidateDiagnostic(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	var packet struct {
		Cases []struct {
			ID           string
			Replace      []string
			Replacements [][]string
			Append       string
			RemovePrefix string `json:"remove_prefix"`
			CRLF         bool
			LegacyPass   bool `json:"legacy_pass"`
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "agent_wiki/assessment_candidates/handover/packet.json"))
	if os.IsNotExist(err) {
		t.Skip("inactive candidate is not part of the exported launcher")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(root, "agent_wiki/assessment_candidates/handover/diagnostic-document.md"))
	if err != nil {
		t.Fatal(err)
	}
	valid := strings.ReplaceAll(string(raw), "REVIEW_DATE", time.Now().UTC().AddDate(0, 0, 30).Format("2006-01-02"))
	missionRaw, err := os.ReadFile(filepath.Join(root, "curriculum/compatibility/5.0.0.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mission missionverify.Mission
	if err = loadLegacyHandoverMission(missionRaw, &mission); err != nil {
		t.Fatal(err)
	}
	for _, c := range packet.Cases {
		text := valid
		if len(c.Replace) == 2 {
			text = strings.ReplaceAll(text, c.Replace[0], c.Replace[1])
		}
		for _, pair := range c.Replacements {
			text = strings.ReplaceAll(text, pair[0], pair[1])
		}
		if c.RemovePrefix != "" {
			kept := []string{}
			for _, line := range strings.Split(text, "\n") {
				if !strings.HasPrefix(line, c.RemovePrefix) {
					kept = append(kept, line)
				}
			}
			text = strings.Join(kept, "\n")
		}
		text += c.Append
		if c.CRLF {
			text = strings.ReplaceAll(text, "\n", "\r\n")
		}
		parent := t.TempDir()
		target := filepath.Join(parent, "practice", "workspace/handover/project-handover.md")
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(target, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		server := &Server{repository: passportrepo.Repository{Root: filepath.Join(parent, ".transport")}}
		receipt, err := server.localReceipt(mission, nil)
		if err != nil {
			t.Fatal(err)
		}
		passed := receipt["passed"].(bool)
		if passed != c.LegacyPass {
			t.Fatalf("%s receipt=%#v", c.ID, receipt)
		}
		after, _ := os.ReadFile(target)
		if string(after) != text {
			t.Fatal("checker changed document")
		}
		result, _ := json.Marshal(map[string]any{"id": c.ID, "passed": passed})
		fmt.Println("HANDOVER_DOC:" + string(result))
	}
}

func TestHandoverCandidateQuestions(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	raw, err := os.ReadFile(filepath.Join(root, "agent_wiki/assessment_candidates/handover/packet.json"))
	if os.IsNotExist(err) {
		t.Skip("inactive candidate is unavailable in the exported launcher")
	}
	if err != nil {
		t.Fatal(err)
	}
	var packet struct {
		Questions   []missionverify.Question
		AnswerCases []struct {
			ID      string
			Answers map[string][]string
		} `json:"answer_cases"`
	}
	if err = json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(root, "curriculum/compatibility/5.0.0.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mission missionverify.Mission
	if err = loadLegacyHandoverMission(raw, &mission); err != nil {
		t.Fatal(err)
	}
	mission.Activity.Questions = packet.Questions
	mission.Verification.PassPercent = 100
	catalog := missionverify.Catalog{Missions: map[string]missionverify.Mission{"data-handover": mission}}
	for _, c := range packet.AnswerCases {
		_, result, err := missionverify.Grade(catalog, missionverify.Attempt{Mission: "data-handover", Answers: c.Answers})
		out, _ := json.Marshal(map[string]any{"id": c.ID, "passed": err == nil && result.Status == "ready_to_submit", "score": result.ScorePercent, "critical": result.CriticalPassed})
		fmt.Println("HANDOVER_GRADE:" + string(out))
	}
}

func loadLegacyHandoverMission(raw []byte, mission *missionverify.Mission) error {
	var snapshot struct {
		Missions map[string]struct{ Metadata missionverify.Mission }
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return err
	}
	*mission = snapshot.Missions["data-handover"].Metadata
	return nil
}
