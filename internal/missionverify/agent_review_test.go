package missionverify

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestAgentReviewConcordance(t *testing.T) {
	raw, err := os.ReadFile("testdata/agent-review.json")
	if err != nil {
		t.Fatal(err)
	}
	var packet struct {
		Mission Mission
		Cases   []struct {
			ID       string
			Answers  json.RawMessage
			Score    int
			Critical bool
			Passed   bool
			Invalid  bool
		}
	}
	if err = json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	catalog := Catalog{Missions: map[string]Mission{"ai-agent-baseline": packet.Mission}}
	for _, c := range packet.Cases {
		var answers map[string][]string
		err = json.Unmarshal(c.Answers, &answers)
		result := Result{}
		if err == nil {
			_, result, err = Grade(catalog, Attempt{Mission: "ai-agent-baseline", Answers: answers})
		}
		passed := err == nil && result.Status == "ready_to_submit"
		record, _ := json.Marshal(map[string]any{"id": c.ID, "score": result.ScorePercent, "critical": result.CriticalPassed, "passed": passed, "invalid": err != nil, "feedback": result.Feedback})
		fmt.Println("AGENT_REVIEW:" + string(record))
		if c.Invalid {
			if passed {
				t.Fatalf("%s accepted malformed answer", c.ID)
			}
			continue
		}
		if err != nil || result.ScorePercent != c.Score || result.CriticalPassed != c.Critical || passed != c.Passed {
			t.Fatalf("%s: %+v %v", c.ID, result, err)
		}
	}
}
