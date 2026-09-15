package localserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/missionverify"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/passportrepo"
)

// Build the synthetic document from the real lesson, not a second schema in
// this test. The Python controller suite consumes the same template and cases.
func TestHandoverReceiptFollowsLessonContract(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../../../.."))
	missionPath := filepath.Join(root, "curriculum/missions/data-handover/mission.json")
	templatePath := filepath.Join(root, "platform/passport-template/fixtures/data_steward/workspace/handover/project-handover.md")
	missionJSON, missionErr := os.ReadFile(missionPath)
	template, templateErr := os.ReadFile(templatePath)
	if os.IsNotExist(missionErr) && os.IsNotExist(templateErr) {
		t.Skip("handbook source contract is unavailable in a standalone launcher export; unit tests still run")
	}
	if missionErr != nil || templateErr != nil {
		t.Fatalf("read handover source: mission=%v template=%v", missionErr, templateErr)
	}
	var mission missionverify.Mission
	var lesson struct {
		Activity struct {
			Recipe struct {
				Steps []struct {
					Commands []struct{ Mode, Command string }
				}
			}
		}
	}
	if err := json.Unmarshal(missionJSON, &mission); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(missionJSON, &lesson); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(template), "\n")
	for _, step := range lesson.Activity.Recipe.Steps {
		for _, command := range step.Commands {
			if command.Mode != "edit" {
				continue
			}
			for _, replacement := range strings.Split(command.Command, "\n") {
				label, _, ok := strings.Cut(replacement, ":")
				if !ok {
					continue
				}
				found := false
				for i, line := range lines {
					if strings.HasPrefix(line, label+":") {
						lines[i], found = replacement, true
					}
				}
				if !found {
					t.Fatalf("lesson field %q is absent from template", label)
				}
			}
		}
	}
	valid := strings.ReplaceAll(strings.Join(lines, "\n"), "YYYY-MM-DD", time.Now().UTC().AddDate(0, 0, 30).Format("2006-01-02"))
	var cases []struct {
		Name, Append   string
		Replace        []string
		RemovePrefix   string   `json:"remove_prefix"`
		CRLF           bool     `json:"crlf"`
		NoFinalNewline bool     `json:"no_final_newline"`
		FailedChecks   []string `json:"failed_checks"`
	}
	data, err := os.ReadFile("testdata/handover-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			text := valid
			if len(test.Replace) == 2 {
				text = strings.ReplaceAll(text, test.Replace[0], test.Replace[1])
			}
			if test.RemovePrefix != "" {
				kept := []string{}
				for _, line := range strings.Split(text, "\n") {
					if !strings.HasPrefix(line, test.RemovePrefix) {
						kept = append(kept, line)
					}
				}
				text = strings.Join(kept, "\n")
			}
			text += test.Append
			if test.NoFinalNewline {
				text = strings.TrimRight(text, "\n")
			}
			if test.CRLF {
				text = strings.ReplaceAll(text, "\n", "\r\n")
			}
			parent := t.TempDir()
			path := filepath.Join(parent, "practice", mission.Submission.ArtifactPaths[0])
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			server := &Server{repository: passportrepo.Repository{Root: filepath.Join(parent, ".transport")}}
			// Exercise the receipt that the submission path uses, including its
			// exact curriculum check names, rather than only the regexp helper.
			receipt, err := server.localReceipt(mission, nil)
			if err != nil {
				t.Fatal(err)
			}
			if receipt["passed"] != (len(test.FailedChecks) == 0) {
				t.Errorf("unexpected receipt: %#v", receipt)
			}
			for name, passed := range receipt["checks"].(map[string]bool) {
				if passed == slices.Contains(test.FailedChecks, name) {
					t.Errorf("check %s=%v; expected failures %v", name, passed, test.FailedChecks)
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != text {
				t.Fatal("verification changed the practice document")
			}
		})
	}
}
