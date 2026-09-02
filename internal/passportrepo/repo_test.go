package passportrepo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type repositoryRunner struct {
	branch string
}

func (runner repositoryRunner) Run(_ context.Context, _ string, _ string, args ...string) ([]byte, error) {
	command := strings.Join(args, " ")
	switch command {
	case "remote get-url origin":
		return []byte("https://github.com/student/passport-exercises.git\n"), nil
	case "remote get-url upstream":
		return []byte("https://github.com/soheylm-passport-sandbox/passport-exercises.git\n"), nil
	case "branch --show-current":
		return []byte(runner.branch + "\n"), nil
	case "rev-parse HEAD", "rev-parse --verify origin/onboarding/student":
		return []byte(strings.Repeat("a", 40) + "\n"), nil
	case "status --porcelain=v1 --untracked-files=normal":
		return nil, nil
	default:
		return nil, nil
	}
}

func TestParseGitHubRemote(t *testing.T) {
	values := []string{
		"https://github.com/IDEALLab/passport-student.git",
		"git@github.com:IDEALLab/passport-student.git",
		"ssh://git@github.com/IDEALLab/passport-student",
	}
	for _, value := range values {
		owner, name, err := parseGitHubRemote(value)
		if err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		if owner != "IDEALLab" || name != "passport-student" {
			t.Fatalf("unexpected parse for %s: %s/%s", value, owner, name)
		}
	}
}

func TestLoadPassportRequiresAssignedAssessmentBranch(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "passport.json")
	valid := `{"schema_version":2,"curriculum_version":"1.2.0","github_user":"Student-1","source_repository":"soheylm-passport-sandbox/passport-exercises","fork_repository":"Student-1/passport-exercises","assessment_branch":"onboarding/student-1","platform":"linux","responsibilities":[],"missions":["core-orientation"]}`
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	passport, err := loadPassport(path)
	if err != nil {
		t.Fatal(err)
	}
	if passport.AssessmentBranch != "onboarding/student-1" {
		t.Fatalf("unexpected assessment branch %q", passport.AssessmentBranch)
	}
	invalid := `{"schema_version":2,"curriculum_version":"1.2.0","github_user":"Student-1","source_repository":"soheylm-passport-sandbox/passport-exercises","fork_repository":"Student-1/passport-exercises","assessment_branch":"main","platform":"linux","responsibilities":[],"missions":["core-orientation"]}`
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPassport(path); err == nil {
		t.Fatal("expected invalid assessment branch to be rejected")
	}
}

func TestLoadPassportRejectsUsernameThatCannotOwnAssignedBranch(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "passport.json")
	invalid := `{"schema_version":2,"curriculum_version":"1.2.0","github_user":"student_name","source_repository":"soheylm-passport-sandbox/passport-exercises","fork_repository":"student_name/passport-exercises","assessment_branch":"onboarding/student_name","platform":"linux","responsibilities":[],"missions":["core-orientation"]}`
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPassport(path); err == nil || !strings.Contains(err.Error(), "invalid assessment branch") {
		t.Fatalf("expected invalid GitHub username to fail, got %v", err)
	}
}

func TestFindAcceptsOnlyTheAssignedAssessmentBranch(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	passport := `{"schema_version":2,"curriculum_version":"1.2.0","github_user":"student","source_repository":"soheylm-passport-sandbox/passport-exercises","fork_repository":"student/passport-exercises","assessment_branch":"onboarding/student","platform":"linux","responsibilities":[],"missions":["core-orientation"]}`
	if err := os.WriteFile(filepath.Join(directory, "passport.json"), []byte(passport), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Find(directory, repositoryRunner{branch: "onboarding/student"}); err != nil {
		t.Fatalf("assigned branch was rejected: %v", err)
	}
	if _, err := Find(directory, repositoryRunner{branch: "main"}); err == nil || !strings.Contains(err.Error(), "git switch onboarding/student") {
		t.Fatalf("wrong branch did not produce a safe recovery command: %v", err)
	}
}

func TestParseGitHubRemoteRejectsOtherHostsAndExtraPath(t *testing.T) {
	for _, value := range []string{
		"https://evil.example/IDEALLab/passport-student.git",
		"https://github.com/IDEALLab/passport-student/extra",
		"file:///tmp/passport",
	} {
		if _, _, err := parseGitHubRemote(value); err == nil {
			t.Fatalf("expected rejection for %s", value)
		}
	}
}
