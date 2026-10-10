package missionverify

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// One synthetic packet exercises the actual local grader and is also consumed
// by controller concordance tests. No student evidence or GitHub access is used.
func TestGitUnderstandingConcordance(t *testing.T) {
	raw, err := os.ReadFile("testdata/git-understanding.json")
	if err != nil {
		t.Fatal(err)
	}
	var packet struct {
		Mission Mission `json:"mission"`
		Cases   []struct {
			ID      string              `json:"id"`
			Answers map[string][]string `json:"answers"`
			Score   int                 `json:"score"`
			Passed  bool                `json:"passed"`
			Invalid bool                `json:"invalid"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	catalog := Catalog{Missions: map[string]Mission{"git-manual-pr": packet.Mission}}
	for _, c := range packet.Cases {
		_, result, err := Grade(catalog, Attempt{Mission: "git-manual-pr", Answers: c.Answers})
		record := map[string]any{"id": c.ID, "score": result.ScorePercent, "critical": result.CriticalPassed, "passed": result.Status == "ready_to_submit", "invalid": err != nil, "feedback": result.Feedback}
		line, _ := json.Marshal(record)
		fmt.Println("GIT_CASE:" + string(line))
		if c.Invalid {
			if err == nil {
				t.Errorf("%s accepted unknown field", c.ID)
			}
			continue
		}
		if err != nil || result.ScorePercent != c.Score || (result.Status == "ready_to_submit") != c.Passed || result.CriticalPassed != c.Passed {
			t.Errorf("%s: %+v %v", c.ID, result, err)
		}
	}
}
