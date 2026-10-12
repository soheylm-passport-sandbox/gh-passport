package missionverify

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// Reviewed synthetic packet exercises the versioned production Grade path.
func TestEulerArraysCandidateGrade(t *testing.T) {
	raw, err := os.ReadFile("testdata/euler-arrays-candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	var packet struct {
		Mission Mission `json:"mission"`
		Cases   []struct {
			ID      string              `json:"id"`
			Answers map[string][]string `json:"answers"`
			Score   int                 `json:"score"`
			Passed  bool                `json:"local_pass"`
			Invalid bool                `json:"invalid"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	catalog := Catalog{Missions: map[string]Mission{"euler-cpu-arrays": packet.Mission}}
	for _, c := range packet.Cases {
		_, result, err := Grade(catalog, Attempt{Mission: "euler-cpu-arrays", Answers: c.Answers})
		passed := err == nil && result.Status == "ready_to_submit"
		record := map[string]any{"id": c.ID, "score": result.ScorePercent, "critical": result.CriticalPassed, "passed": passed, "invalid": err != nil, "feedback": result.Feedback}
		line, _ := json.Marshal(record)
		fmt.Println("EULER_GRADE:" + string(line))
		if c.Invalid {
			if err == nil {
				t.Errorf("%s accepted unknown field", c.ID)
			}
			continue
		}
		if err != nil || result.ScorePercent != c.Score || passed != c.Passed || result.CriticalPassed != c.Passed {
			t.Errorf("%s: %+v %v", c.ID, result, err)
		}
	}
}
