package localserver

import (
	"encoding/json"
	"io"
	"net/http"
	"runtime"
)

// ComputerContext describes this launcher, never a change to the frozen route.
// No machine identifier, local path or credentials enter the learning record.
type ComputerContext struct {
	Platform         string   `json:"platform"`
	OriginalPlatform string   `json:"original_platform"`
	SetupMissions    []string `json:"setup_missions"`
}

var computerSetupMissions = []string{"git-tools-auth", "python-environment", "euler-access-ssh"}

func (server *Server) computerContext() ComputerContext {
	platform := server.hostPlatform
	if platform == "" {
		platform = runtime.GOOS
	}
	if platform == "darwin" {
		platform = "macos"
	}
	value := ComputerContext{Platform: platform, OriginalPlatform: server.repository.Passport.Platform, SetupMissions: []string{}}
	for _, candidate := range computerSetupMissions {
		for _, assigned := range server.repository.Passport.Missions {
			if candidate == assigned {
				value.SetupMissions = append(value.SetupMissions, candidate)
				break
			}
		}
	}
	return value
}

// recheckComputer deliberately bypasses grading, submission and local attempt
// state. Only these bounded setup diagnostics may revisit a completed lesson.
// In particular, the SSH diagnostic does not attest a successful live login.
func (server *Server) recheckComputer(response http.ResponseWriter, request *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	request.Body = http.MaxBytesReader(response, request.Body, 1024)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		Mission string `json:"mission"`
	}
	if err := decoder.Decode(&input); err != nil {
		server.writeError(response, http.StatusBadRequest, "invalid_computer_check")
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		server.writeError(response, http.StatusBadRequest, "invalid_computer_check")
		return
	}
	computer := server.computerContext()
	allowed := false
	for _, mission := range computer.SetupMissions {
		allowed = allowed || mission == input.Mission
	}
	if !allowed {
		server.writeError(response, http.StatusForbidden, "computer_check_outside_setup")
		return
	}
	var checks map[string]bool
	switch input.Mission {
	case "git-tools-auth":
		checks = server.verifyGitEnvironment()
	case "python-environment":
		checks = server.verifyPythonEnvironment()
	case "euler-access-ssh":
		checks = server.verifySSHConfig()
	}
	passed := len(checks) > 0
	for _, ok := range checks {
		passed = passed && ok
	}
	server.writeJSON(response, http.StatusOK, map[string]any{
		"mission": input.Mission, "platform": computer.Platform,
		"checks": checks, "passed": passed, "assessment_changed": false,
	})
}
