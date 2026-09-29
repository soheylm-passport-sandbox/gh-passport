package missionverify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gradingCatalog() Catalog {
	return Catalog{
		SchemaVersion:     2,
		CurriculumVersion: "2.0.0",
		Missions: map[string]Mission{
			"safe-task": {
				Activity: Activity{Questions: []Question{
					{ID: "critical", Type: "single", Options: []Option{{ID: "yes"}, {ID: "no"}}, Answer: []string{"yes"}, Critical: true, Feedback: "Choose the safe action."},
					{ID: "sequence", Type: "order", Options: []Option{{ID: "first"}, {ID: "second"}}, Answer: []string{"first", "second"}, Feedback: "Preserve the order."},
				}},
				Verification: Verification{LocalVerifier: "knowledge_check", PassPercent: 80, CriticalMustPass: true, ReceiptChecks: []string{"structured_activity"}},
			},
		},
	}
}

func TestGradeRequiresEveryCriticalAnswerAndOrderedSequence(t *testing.T) {
	catalog := gradingCatalog()
	_, passed, err := Grade(catalog, Attempt{Mission: "safe-task", Answers: map[string][]string{
		"critical": {"yes"}, "sequence": {"first", "second"},
	}})
	if err != nil || passed.Status != "ready_to_submit" || passed.ScorePercent != 100 || !passed.CriticalPassed {
		t.Fatalf("valid attempt = %#v, %v", passed, err)
	}

	_, failed, err := Grade(catalog, Attempt{Mission: "safe-task", Answers: map[string][]string{
		"critical": {"no"}, "sequence": {"second", "first"},
	}})
	if err != nil || failed.Status != "needs_work" || failed.CriticalPassed {
		t.Fatalf("unsafe attempt = %#v, %v", failed, err)
	}
}

func TestGradeRejectsUnknownAnswerFields(t *testing.T) {
	_, _, err := Grade(gradingCatalog(), Attempt{Mission: "safe-task", Answers: map[string][]string{
		"critical": {"yes"}, "sequence": {"first", "second"}, "token": {"wrong"},
	}})
	if err == nil || !strings.Contains(err.Error(), "unknown answer") {
		t.Fatalf("unknown field was accepted: %v", err)
	}
}

func TestNASProbeIsBoundedRemovedAndDoesNotExposePath(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "student")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	mission := Mission{Verification: Verification{LocalVerifier: "nas_access", RequiresLiveConfirmation: true}}
	receipt, err := ConfirmLive(mission, map[string]string{"path": root, "username": "student"})
	if err != nil || receipt["passed"] != true {
		t.Fatalf("NAS probe = %#v, %v", receipt, err)
	}
	encodedReceipt, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := strings.ReplaceAll(strings.TrimSpace(root), "\\", "/")
	if strings.Contains(strings.ReplaceAll(string(encodedReceipt), "\\", "/"), privatePath) {
		t.Fatal("NAS receipt exposed the private path")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("NAS probe left files behind: %#v, %v", entries, err)
	}
}

func TestEulerJobReceiptRequiresEveryObservedBoundary(t *testing.T) {
	mission := Mission{Verification: Verification{LocalVerifier: "euler_job", RequiresLiveConfirmation: true}}
	valid := map[string]string{
		"job_id": "123456", "account": "es_fuge", "owner_checked": "yes",
		"queue_inspected": "yes", "state": "COMPLETED", "exit_code": "0:0",
		"alloc_cpus": "1", "req_mem": "1Gc", "seff_seen": "yes",
		"python_environment": "yes", "output_marker": "5 squared is 25",
	}
	receipt, err := ConfirmLive(mission, valid)
	if err != nil || receipt["passed"] != true {
		t.Fatalf("valid Euler observation = %#v, %v", receipt, err)
	}
	for key := range valid {
		forged := make(map[string]string, len(valid))
		for name, value := range valid {
			forged[name] = value
		}
		forged[key] = "wrong"
		receipt, err = ConfirmLive(mission, forged)
		if err != nil || receipt["passed"] == true {
			t.Fatalf("invalid %s passed: %#v, %v", key, receipt, err)
		}
	}
}

func TestEulerJobReceiptAcceptsOnlyLabNormalAccountAndZeroExit(t *testing.T) {
	mission := Mission{Verification: Verification{LocalVerifier: "euler_job", RequiresLiveConfirmation: true}}
	base := map[string]string{
		"job_id": "123456", "owner_checked": "yes", "queue_inspected": "yes",
		"state": "COMPLETED", "alloc_cpus": "1", "req_mem": "1Gc",
		"seff_seen": "yes", "python_environment": "yes", "output_marker": "5 squared is 25",
	}
	for _, test := range []struct {
		account  string
		exitCode string
		want     bool
	}{
		{"es_fuge", "0:0", true},
		{"normal/es_fuge", "0:0", true},
		{" normal/es_fuge ", " 0:0 ", true},
		{"bulk/es_fuge", "0:0", false},
		{"normal/es+", "0:0", false},
		{"normal/es_other", "0:0", false},
		{"normal/es_fuge", "1:0", false},
		{"normal/es_fuge", "0:9", false},
	} {
		input := make(map[string]string, len(base)+2)
		for key, value := range base {
			input[key] = value
		}
		input["account"] = test.account
		input["exit_code"] = test.exitCode
		receipt, err := ConfirmLive(mission, input)
		if err != nil || receipt["passed"] != test.want {
			t.Errorf("account=%q exit=%q: receipt=%#v err=%v", test.account, test.exitCode, receipt, err)
		}
	}
}

func TestEulerPythonReceiptRequiresExactSafeMarker(t *testing.T) {
	mission := Mission{Verification: Verification{LocalVerifier: "euler_python", RequiresLiveConfirmation: true}}
	receipt, err := ConfirmLive(mission, map[string]string{"environment_marker": "euler-python-env-ok"})
	if err != nil || receipt["passed"] != true {
		t.Fatalf("valid Euler Python marker = %#v, %v", receipt, err)
	}
	receipt, err = ConfirmLive(mission, map[string]string{"environment_marker": "almost-ok"})
	if err != nil || receipt["passed"] == true {
		t.Fatalf("invalid Euler Python marker passed = %#v, %v", receipt, err)
	}
}

func TestAIConfigurationRequiresTheObservedReadOnlyResult(t *testing.T) {
	mission := Mission{Verification: Verification{LocalVerifier: "ai_configuration", RequiresLiveConfirmation: true}}
	receipt, err := ConfirmLive(mission, map[string]string{"agent_response_seen": "yes"})
	if err != nil || receipt["passed"] != true {
		t.Fatalf("observed read-only result = %#v, %v", receipt, err)
	}
	receipt, err = ConfirmLive(mission, map[string]string{"agent_response_seen": "no"})
	if err != nil || receipt["passed"] == true {
		t.Fatalf("unobserved read-only result passed = %#v, %v", receipt, err)
	}
}
