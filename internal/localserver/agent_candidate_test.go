package localserver

import (
	"encoding/json"
	"fmt"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/missionverify"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/passportrepo"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func agentCandidateSource(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	if _, err := os.Stat(filepath.Join(root, "agent_wiki/assessment_candidates/agent_baseline/packet.json")); os.IsNotExist(err) {
		t.Skip("inactive candidate not exported")
	}
	return root
}

func TestAgentCandidateQuestions(t *testing.T) {
	root := agentCandidateSource(t)
	var packet struct {
		Questions   []missionverify.Question
		AnswerCases []struct {
			ID      string
			Answers map[string][]string
		} `json:"answer_cases"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "agent_wiki/assessment_candidates/agent_baseline/packet.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(root, "curriculum/compatibility/6.0.0.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mission missionverify.Mission
	var frozen struct {
		Missions map[string]struct{ Metadata missionverify.Mission }
	}
	if err = json.Unmarshal(raw, &frozen); err != nil {
		t.Fatal(err)
	}
	mission = frozen.Missions["ai-agent-baseline"].Metadata
	mission.Activity.Questions = packet.Questions
	mission.Verification.PassPercent = 100
	catalog := missionverify.Catalog{Missions: map[string]missionverify.Mission{"ai-agent-baseline": mission}}
	for _, c := range packet.AnswerCases {
		_, result, err := missionverify.Grade(catalog, missionverify.Attempt{Mission: "ai-agent-baseline", Answers: c.Answers})
		out, _ := json.Marshal(map[string]any{"id": c.ID, "passed": err == nil && result.Status == "ready_to_submit", "score": result.ScorePercent, "critical": result.CriticalPassed})
		fmt.Println("AGENT_GRADE:" + string(out))
	}
}

// Diagnose real Git scope; do not change any installed receipt meaning here.
func TestAgentCandidateGitBoundaries(t *testing.T) {
	source := agentCandidateSource(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, name := range []string{"valid", "outside_tracked", "untracked_inside", "outside_untracked", "staged_allowed", "prior_work_preserved", "changed_canary", "extra_tracked_inside", "canary_symlink"} {
		t.Run(name, func(t *testing.T) {
			if name == "canary_symlink" && runtime.GOOS == "windows" {
				t.Skip("no privilege change for symlink")
			}
			parent := t.TempDir()
			root := filepath.Join(parent, "practice")
			task := filepath.Join(root, "workspace/agent_task")
			if err := os.MkdirAll(task, 0700); err != nil {
				t.Fatal(err)
			}
			git := func(dir string, args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = dir
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %s %v", args, out, err)
				}
				return string(out)
			}
			write := func(path, text string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(root, "outside.md"), "Synthetic outside baseline\n")
			for _, file := range []string{"README.md", "storage-plan.md", "scope-canary.txt"} {
				raw, err := os.ReadFile(filepath.Join(source, "platform/passport-template/fixtures/ai_agents/workspace/agent_task", file))
				if err != nil {
					t.Fatal(err)
				}
				// The source checkout may use CRLF on Windows. This owned
				// synthetic fixture represents the original LF contract;
				// never normalize a learner file or relax its canary check.
				write(filepath.Join(task, file), strings.ReplaceAll(string(raw), "\r\n", "\n"))
			}
			git(root, "init")
			git(root, "config", "core.autocrlf", "false")
			git(root, "add", ".")
			git(root, "-c", "user.name=Passport Example", "-c", "user.email=example@invalid.test", "-c", "core.hooksPath="+os.DevNull, "commit", "-m", "docs(training): fictional baseline")
			raw, err := os.ReadFile(filepath.Join(source, "tools/passport-cli/internal/localserver/testdata/agent-cases.json"))
			if err != nil {
				t.Fatal(err)
			}
			var corpus struct{ Base string }
			if err = json.Unmarshal(raw, &corpus); err != nil {
				t.Fatal(err)
			}
			if name == "prior_work_preserved" {
				write(filepath.Join(root, "outside.md"), "Earlier legitimate synthetic work\n")
			}
			beforeStatus := git(root, "status", "--short", "--untracked-files=all")
			beforeOutside := git(root, "diff", "HEAD", "--", "outside.md")
			write(filepath.Join(task, "storage-plan.md"), corpus.Base)
			switch name {
			case "outside_tracked":
				write(filepath.Join(root, "outside.md"), "Unexpected synthetic change\n")
			case "untracked_inside":
				write(filepath.Join(task, "notes.txt"), "Unexpected synthetic note\n")
			case "outside_untracked":
				write(filepath.Join(root, "notes.txt"), "Unexpected synthetic note\n")
			case "staged_allowed":
				git(root, "add", "workspace/agent_task/storage-plan.md")
			case "changed_canary":
				write(filepath.Join(task, "scope-canary.txt"), "changed\n")
			case "extra_tracked_inside":
				write(filepath.Join(task, "README.md"), "changed\n")
			case "canary_symlink":
				raw, _ := os.ReadFile(filepath.Join(task, "scope-canary.txt"))
				write(filepath.Join(root, "canary-target"), string(raw))
				if err := os.Remove(filepath.Join(task, "scope-canary.txt")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, "canary-target"), filepath.Join(task, "scope-canary.txt")); err != nil {
					t.Fatal(err)
				}
			}
			server := &Server{repository: passportrepo.Repository{Root: filepath.Join(parent, ".transport")}, runner: passportrepo.ExecRunner{}}
			checks := server.verifyAgentProject()
			passed := true
			for _, v := range checks {
				passed = passed && v
			}
			out, _ := json.Marshal(map[string]any{"id": name, "passed": passed, "checks": checks, "before_status": beforeStatus, "before_outside_diff": beforeOutside, "after_outside_diff": git(root, "diff", "HEAD", "--", "outside.md"), "status": git(root, "status", "--short", "--untracked-files=all"), "wrong_subfolder_status": git(task, "status", "--short", "--", "workspace/agent_task"), "unstaged_diff": git(root, "diff", "--", "workspace/agent_task"), "head_diff": git(root, "diff", "HEAD", "--", "workspace/agent_task")})
			fmt.Println("AGENT_SCOPE:" + string(out))
		})
	}
}
