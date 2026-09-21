package localserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/githubstatus"
)

func TestComputerSwitchKeepsProtectedPassportAndDrafts(t *testing.T) {
	server := testServer(t)
	server.repository.Passport.Platform = "macos"
	server.repository.Passport.Missions = []string{"git-tools-auth", "python-environment", "euler-access-ssh", "ai-agent-baseline"}
	protected, _ := json.MarshalIndent(server.repository.Passport, "", " ")
	path := filepath.Join(server.repository.Root, "passport.json")
	if err := os.WriteFile(path, protected, 0o600); err != nil {
		t.Fatal(err)
	}
	state := server.defaultState()
	state.MissionDrafts = map[string]map[string][]string{"git-tools-auth": {"example": {"kept"}}}
	state.AttemptCounts = map[string]int{"git-tools-auth": 7}
	state.LaunchCount = 4
	if err := server.store.Save(state); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(server.store.Path())
	for _, platform := range []string{"macos", "linux", "windows", "macos"} {
		server.hostPlatform = platform
		response := request(server, http.MethodGet, "/__passport/v1/context", nil, true, false)
		var payload ContextPayload
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Computer.Platform != platform || payload.Repository.Passport.Platform != "macos" {
			t.Fatalf("wrong computer/record: %#v", payload)
		}
		if !reflect.DeepEqual(payload.Computer.SetupMissions, computerSetupMissions) {
			t.Fatalf("setup scope: %#v", payload.Computer)
		}
		wantState, _ := json.Marshal(state)
		gotState, _ := json.Marshal(payload.LocalState)
		if !bytes.Equal(gotState, wantState) {
			t.Fatal("switch changed draft or attempt state")
		}
	}
	after, _ := os.ReadFile(server.store.Path())
	raw, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || !bytes.Equal(raw, protected) {
		t.Fatal("computer detection changed stored files")
	}
}

func TestComputerRecheckIsNotAnAssessmentAndDoesNotConsumeAttempts(t *testing.T) {
	server := testServer(t)
	server.repository.Passport.Missions = []string{"euler-access-ssh", "python-environment", "git-tools-auth", "ai-agent-baseline"}
	server.hostPlatform = "linux"
	server.repository.Passport.Platform = "macos"
	state := server.defaultState()
	state.AttemptCounts = map[string]int{"euler-access-ssh": 5}
	state.MissionDrafts = map[string]map[string][]string{"euler-access-ssh": {"kept": {"draft"}}}
	if err := server.store.Save(state); err != nil {
		t.Fatal(err)
	}
	server.official = &githubstatus.Official{}
	before, _ := os.ReadFile(server.store.Path())
	beforeOfficial, _ := json.Marshal(server.official)
	// This fake runner does not touch credentials, the network or Euler.
	server.runner = sshConfigRunner{output: "hostname euler.ethz.ch\nport 22\nuser example\nidentityfile ~/.ssh/id_ed25519\nidentitiesonly yes\npreferredauthentications publickey\npasswordauthentication no\nkbdinteractiveauthentication no\nforwardagent no\n"}
	body := []byte(`{"mission":"euler-access-ssh"}`)
	response := request(server, http.MethodPost, "/__passport/v2/computer-check", body, true, true)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	var result struct {
		Passed            bool            `json:"passed"`
		AssessmentChanged bool            `json:"assessment_changed"`
		Checks            map[string]bool `json:"checks"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.AssessmentChanged {
		t.Fatal(response.Body.String())
	}
	if _, exists := result.Checks["key_only_marker"]; exists {
		t.Fatal("configuration check forged a live login receipt")
	}
	after, _ := os.ReadFile(server.store.Path())
	afterOfficial, _ := json.Marshal(server.official)
	if !bytes.Equal(before, after) || !bytes.Equal(beforeOfficial, afterOfficial) {
		t.Fatal("recheck changed assessment or navigation state")
	}
	if _, err := os.Stat(filepath.Join(server.repository.Root, "submissions")); !os.IsNotExist(err) {
		t.Fatal("recheck created a submission")
	}
	server.runner = sshConfigRunner{err: os.ErrNotExist}
	response = request(server, http.MethodPost, "/__passport/v2/computer-check", body, true, true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"passed":false`) {
		t.Fatal("failed diagnostic was not reported")
	}
	after, _ = os.ReadFile(server.store.Path())
	if !bytes.Equal(before, after) {
		t.Fatal("failed diagnostic changed saved work")
	}
}

func TestComputerChecksEnforceSessionOriginAndFixedScope(t *testing.T) {
	server := testServer(t)
	server.repository.Passport.Missions = []string{"euler-access-ssh", "ai-agent-baseline"}
	for _, tc := range []struct {
		body            string
		session, origin bool
		code            int
	}{
		{`{"mission":"euler-access-ssh"}`, false, true, http.StatusUnauthorized},
		{`{"mission":"euler-access-ssh"}`, true, false, http.StatusForbidden},
		{`{"mission":"ai-agent-baseline"}`, true, true, http.StatusForbidden},
		{`{"mission":"git-tools-auth"}`, true, true, http.StatusForbidden},
		{`{"mission":"euler-access-ssh","answers":{}}`, true, true, http.StatusBadRequest},
		{`{"mission":"euler-access-ssh"} {}`, true, true, http.StatusBadRequest},
	} {
		response := request(server, http.MethodPost, "/__passport/v2/computer-check", []byte(tc.body), tc.session, tc.origin)
		if response.Code != tc.code {
			t.Fatalf("%s: %d, want %d", tc.body, response.Code, tc.code)
		}
	}
	// The new endpoint does not relax the existing assessment gate.
	response := request(server, http.MethodPost, "/__passport/v2/verify", []byte(`{"mission":"ai-agent-baseline","answers":{}}`), true, true)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "mission_not_current") {
		t.Fatal(response.Body.String())
	}
}
