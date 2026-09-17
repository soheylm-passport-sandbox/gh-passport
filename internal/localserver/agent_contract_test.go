package localserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/agentverify"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/missionverify"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/passportrepo"
)

func TestAgentReceiptMatchesSharedControllerCases(t *testing.T) {
	var corpus struct {
		Base  string
		Cases []struct {
			Name           string
			Text           *string
			Replace        [][]string
			Append         string
			Uppercase      bool
			CRLF           bool
			NoFinalNewline bool     `json:"no_final_newline"`
			FailedChecks   []string `json:"failed_checks"`
		}
	}
	data, err := os.ReadFile("testdata/agent-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	// The seven receipt fields stay compatible with existing curriculum 2.1.2.
	mission := missionverify.Mission{}
	mission.Verification.LocalVerifier = "ai_project"
	mission.Verification.ReceiptChecks = []string{"bounded_file", "bounded_diff", "canary_unchanged", "durable_p", "temporary_d", "avoid_c", "heavy_compute"}
	_, source, _, _ := runtime.Caller(0)
	missionPath := filepath.Join(filepath.Dir(source), "../../../../curriculum/missions/ai-agent-baseline/mission.json")
	if data, err := os.ReadFile(missionPath); err == nil {
		if err := json.Unmarshal(data, &mission); err != nil {
			t.Fatal(err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	// The shared cases also run in a standalone launcher export, without source curriculum.
	for _, test := range corpus.Cases {
		t.Run(test.Name, func(t *testing.T) {
			text := corpus.Base
			if test.Text != nil {
				text = *test.Text
			}
			for _, replacement := range test.Replace {
				if len(replacement) != 2 || !strings.Contains(text, replacement[0]) {
					t.Fatal("invalid case replacement")
				}
				text = strings.ReplaceAll(text, replacement[0], replacement[1])
			}
			text += test.Append
			if test.Uppercase {
				text = strings.ToUpper(text)
			}
			if test.NoFinalNewline {
				text = strings.TrimRight(text, "\n")
			}
			if test.CRLF {
				text = strings.ReplaceAll(text, "\n", "\r\n")
			}
			parent := t.TempDir()
			path := filepath.Join(parent, "practice/workspace/agent_task/storage-plan.md")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(filepath.Dir(path), "scope-canary.txt"), []byte("IDEAL-PASSPORT-AGENT-SCOPE-CANARY-v1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			server := &Server{repository: passportrepo.Repository{Root: filepath.Join(parent, ".transport")}, runner: agentProjectRunner{changed: "workspace/agent_task/storage-plan.md\n"}}
			receipt, err := server.localReceipt(mission, nil)
			if err != nil {
				t.Fatal(err)
			}
			if receipt["passed"] != (len(test.FailedChecks) == 0) {
				t.Errorf("unexpected receipt: %#v", receipt)
			}
			for name, passed := range receipt["checks"].(map[string]bool) {
				if passed == slices.Contains(test.FailedChecks, name) {
					t.Errorf("%s=%v; expected failures %v", name, passed, test.FailedChecks)
				}
			}
			if after, err := os.ReadFile(path); err != nil || string(after) != text {
				t.Fatal("verifier changed the plan")
			}
		})
	}
}

func TestAgentPlanFileBoundaries(t *testing.T) {
	for _, kind := range []string{"missing", "directory", "symlink", "parent-symlink", "oversized", "invalid-utf8", "nul"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "workspace/agent_task/storage-plan.md")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(path, 0o700)
			case "symlink", "parent-symlink":
				if runtime.GOOS == "windows" {
					t.Skip("symlink test needs Windows privileges; exercised on POSIX")
				}
				sentinel := filepath.Join(root, "sentinel")
				if err = os.WriteFile(sentinel, []byte("Synthetic sentinel"), 0o600); err != nil {
					t.Fatal(err)
				}
				if kind == "parent-symlink" {
					if err = os.Remove(filepath.Dir(path)); err != nil {
						t.Fatal(err)
					}
					err = os.Symlink(root, filepath.Dir(path))
				} else {
					err = os.Symlink(sentinel, path)
				}
			case "oversized":
				err = os.WriteFile(path, []byte(strings.Repeat("x", 100000)), 0o600)
			case "invalid-utf8":
				err = os.WriteFile(path, []byte{0xff}, 0o600)
			case "nul":
				err = os.WriteFile(path, []byte{0}, 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			for name, passed := range agentverify.CheckPlan(root) {
				if passed {
					t.Errorf("unsafe file passed %s", name)
				}
			}
		})
	}
}
