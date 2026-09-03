package githubstatus

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/passportrepo"
)

type fakeRunner struct {
	responses [][]byte
	calls     [][]string
}

func (runner *fakeRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	runner.calls = append(runner.calls, args)
	if len(runner.responses) == 0 {
		return nil, fmt.Errorf("unexpected gh call")
	}
	value := runner.responses[0]
	runner.responses = runner.responses[1:]
	return value, nil
}

func statusMarker(t *testing.T, sha string, completed ...bool) string {
	t.Helper()
	stage := "blocked"
	missionStatus := "needs_work"
	var currentMission any = "core-orientation"
	if len(completed) == 1 && completed[0] {
		stage = "complete"
		missionStatus = "passed"
		currentMission = nil
	}
	value := map[string]any{
		"schema_version":     2,
		"curriculum_version": "2.0.0",
		"head_sha":           sha,
		"current_mission":    currentMission,
		"missions":           []map[string]string{{"id": "core-orientation", "status": missionStatus}},
		"stage":              stage,
		"review_state":       "not_ready",
		"assessed_at":        "2026-08-30T12:00:00Z",
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return "<!-- ideal-passport-status:v2:" + base64.RawURLEncoding.EncodeToString(raw) + " -->"
}

func TestSyncAcceptsClosedUnmergedPROnlyAfterTrustedCompletion(t *testing.T) {
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pulls, _ := json.Marshal([]map[string]any{{
		"number": 7, "html_url": "https://github.com/soheylm-passport-sandbox/passport-exercises/pull/7", "state": "closed", "merged": false,
		"head": map[string]string{"ref": "onboarding/student", "sha": sha},
	}})
	checks, _ := json.Marshal(map[string]any{"check_runs": []map[string]any{{
		"id": 9, "name": checkName, "head_sha": sha, "status": "completed", "conclusion": "success",
		"completed_at": "2026-08-30T12:00:01Z", "app": map[string]any{"id": 42},
		"output": map[string]string{"summary": statusMarker(t, sha, true), "text": ""},
	}}})
	repository := passportrepo.Repository{
		Root: ".", Owner: "student", Name: "passport-exercises", UpstreamOwner: "soheylm-passport-sandbox", UpstreamName: "passport-exercises", Branch: "onboarding/student",
		Passport: passportrepo.Passport{CurriculumVersion: "2.0.0", Missions: []string{"core-orientation"}},
	}
	result, err := Sync(context.Background(), repository, 42, &fakeRunner{responses: [][]byte{pulls, checks}})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StateVerified || result.Official == nil || result.Official.Status.Stage != "complete" {
		t.Fatalf("closed completed passport did not resume: %#v", result)
	}

	incompleteChecks, _ := json.Marshal(map[string]any{"check_runs": []map[string]any{{
		"id": 10, "name": checkName, "head_sha": sha, "status": "completed", "conclusion": "action_required",
		"completed_at": "2026-08-30T12:00:02Z", "app": map[string]any{"id": 42},
		"output": map[string]string{"summary": statusMarker(t, sha), "text": ""},
	}}})
	result, err = Sync(context.Background(), repository, 42, &fakeRunner{responses: [][]byte{pulls, incompleteChecks}})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StateRecoveryRequired || result.Official != nil {
		t.Fatalf("closed incomplete passport was trusted: %#v", result)
	}
}

func TestSyncRequiresTrustedAppAndExactSHA(t *testing.T) {
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pulls, _ := json.Marshal([]map[string]any{{
		"number": 7, "html_url": "https://github.com/soheylm-passport-sandbox/passport-exercises/pull/7", "state": "open", "merged": false,
		"url":  "https://api.github.com/repos/soheylm-passport-sandbox/passport-exercises/pulls/7",
		"head": map[string]string{"ref": "onboarding/student", "sha": sha},
	}})
	checks, _ := json.Marshal(map[string]any{"total_count": 1, "check_runs": []map[string]any{{
		"id": 9, "name": checkName, "head_sha": sha, "status": "completed", "conclusion": "action_required",
		"url":          "https://api.github.com/repos/soheylm-passport-sandbox/passport-exercises/check-runs/9",
		"completed_at": "2026-08-30T12:00:01Z", "app": map[string]any{"id": 42},
		"output": map[string]string{"summary": statusMarker(t, sha), "text": ""},
	}}})
	runner := &fakeRunner{responses: [][]byte{pulls, checks}}
	repository := passportrepo.Repository{
		Root: ".", Owner: "student", Name: "passport-exercises", UpstreamOwner: "soheylm-passport-sandbox", UpstreamName: "passport-exercises", Branch: "onboarding/student",
		Passport: passportrepo.Passport{CurriculumVersion: "2.0.0", Missions: []string{"core-orientation"}},
	}
	result, err := Sync(context.Background(), repository, 42, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StateVerified || result.Official == nil {
		t.Fatalf("unexpected sync state: %#v", result)
	}
	official := result.Official
	if official.ControllerAppID != 42 || official.RemoteHeadSHA != sha || official.Status.Stage != "blocked" {
		t.Fatalf("unexpected official projection: %#v", official)
	}
	if official.RecoveryIssueURL != "https://github.com/soheylm-passport-sandbox/passport-exercises/issues/new?template=passport-help.yml" {
		t.Fatalf("unexpected recovery URL: %s", official.RecoveryIssueURL)
	}
}

func TestSyncReportsNormalControllerWait(t *testing.T) {
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pulls, _ := json.Marshal([]map[string]any{{
		"number": 7, "html_url": "https://github.com/soheylm-passport-sandbox/passport-exercises/pull/7", "state": "open", "merged": false,
		"head": map[string]string{"ref": "onboarding/student", "sha": sha},
	}})
	checks, _ := json.Marshal(map[string]any{"check_runs": []map[string]any{}})
	runner := &fakeRunner{responses: [][]byte{pulls, checks}}
	repository := passportrepo.Repository{
		Root: ".", Owner: "student", Name: "passport-exercises", UpstreamOwner: "soheylm-passport-sandbox", UpstreamName: "passport-exercises", Branch: "onboarding/student",
		Passport: passportrepo.Passport{CurriculumVersion: "1.2.0", Missions: []string{"core-orientation"}},
	}
	result, err := Sync(context.Background(), repository, 42, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StateWaitingForController || result.Official != nil || result.RemoteHeadSHA != sha {
		t.Fatalf("unexpected pending result: %#v", result)
	}
}

func TestSyncReportsMissingAssessmentForRecovery(t *testing.T) {
	pulls, _ := json.Marshal([]map[string]any{})
	runner := &fakeRunner{responses: [][]byte{pulls}}
	repository := passportrepo.Repository{
		Root: ".", Owner: "student", Name: "passport-exercises", UpstreamOwner: "soheylm-passport-sandbox", UpstreamName: "passport-exercises", Branch: "onboarding/student",
		Passport: passportrepo.Passport{CurriculumVersion: "1.2.0", Missions: []string{"core-orientation"}},
	}
	result, err := Sync(context.Background(), repository, 42, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StateRecoveryRequired || result.RecoveryIssueURL == "" || result.Official != nil {
		t.Fatalf("unexpected recovery result: %#v", result)
	}
}

func TestSyncRejectsSameNamedCheckFromWrongApp(t *testing.T) {
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pulls, _ := json.Marshal([]map[string]any{{
		"number": 7, "html_url": "https://github.com/soheylm-passport-sandbox/passport-exercises/pull/7", "state": "open", "merged": false,
		"head": map[string]string{"ref": "onboarding/student", "sha": sha},
	}})
	checks, _ := json.Marshal(map[string]any{"check_runs": []map[string]any{{
		"id": 9, "name": checkName, "head_sha": sha, "status": "completed", "conclusion": "success",
		"completed_at": "2026-08-30T12:00:01Z", "app": map[string]any{"id": 999},
		"output": map[string]string{"summary": statusMarker(t, sha), "text": ""},
	}}})
	runner := &fakeRunner{responses: [][]byte{pulls, checks}}
	repository := passportrepo.Repository{
		Root: ".", Owner: "student", Name: "passport-exercises", UpstreamOwner: "soheylm-passport-sandbox", UpstreamName: "passport-exercises", Branch: "onboarding/student",
		Passport: passportrepo.Passport{CurriculumVersion: "1.2.0", Missions: []string{"core-orientation"}},
	}
	if _, err := Sync(context.Background(), repository, 42, runner); err == nil {
		t.Fatal("expected same-named check from another App to be rejected")
	}
}
