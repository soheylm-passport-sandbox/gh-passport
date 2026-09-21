package starter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Git is real; GitHub responses and both network remotes are isolated fixtures.
type resumeRunner struct {
	fork, upstream    string
	fail, recordState string
	recordResponse    string
	calls             []string
}

func (r *resumeRunner) Run(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
	command := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, command)
	if r.fail != "" && strings.Contains(command, r.fail) {
		return nil, errors.New("simulated interruption")
	}
	if name == "gh" {
		switch {
		case command == "gh api user --jq .login":
			return []byte("student"), nil
		case command == "gh auth setup-git":
			return nil, nil
		case strings.HasPrefix(command, "gh api repos/student/passport-exercises --jq"):
			return []byte("true\t" + sourceRepository), nil
		case strings.Contains(command, "/pulls?state=all"):
			if r.recordResponse != "" {
				return []byte(r.recordResponse), nil
			}
			return []byte(fmt.Sprintf(`[{"state":%q,"html_url":"https://github.com/soheylm-passport-sandbox/passport-exercises/pull/1","merged_at":null}]`, r.recordState)), nil
		default:
			return nil, fmt.Errorf("unexpected GitHub command: %s", command)
		}
	}
	if name != "git" {
		return nil, fmt.Errorf("unexpected executable %s", name)
	}
	// get-url is deliberately read without rewrite configuration: this is the
	// learner-visible URL, while fetch/clone use only the local bare repositories.
	if !(len(args) > 1 && args[0] == "remote" && args[1] == "get-url") {
		args = append([]string{"-c", "url." + filepath.ToSlash(r.fork) + ".insteadOf=https://github.com/student/passport-exercises.git", "-c", "url." + filepath.ToSlash(r.upstream) + ".insteadOf=https://github.com/" + sourceRepository + ".git"}, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", command, err, out)
	}
	return out, nil
}

func resumeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Synthetic Learner", "GIT_AUTHOR_EMAIL=learner@example.invalid", "GIT_COMMITTER_NAME=Synthetic Learner", "GIT_COMMITTER_EMAIL=learner@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func resumeFixture(t *testing.T) (*resumeRunner, string, []byte) {
	t.Helper()
	base := t.TempDir()
	seed := filepath.Join(base, "seed")
	resumeGit(t, base, "init", "-b", "main", seed)
	value := catalog{SchemaVersion: 2, CurriculumVersion: "1.2.0", SourceRepository: sourceRepository, UniversalTracks: []string{"core"}, TrackDependencies: map[string][]string{"core": {}}, Tracks: []track{{ID: "core", Missions: []string{"core-orientation", "euler-access-ssh"}}}}
	raw, _ := json.Marshal(value)
	write := func(name string, content []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(seed, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("passport-curriculum.json", raw)
	write(".gitignore", []byte(".passport-local/\n"))
	resumeGit(t, seed, "add", ".")
	resumeGit(t, seed, "commit", "-m", "test: synthetic catalog")
	upstream := filepath.Join(base, "upstream.git")
	resumeGit(t, base, "clone", "--bare", seed, upstream)
	resumeGit(t, seed, "switch", "-c", "onboarding/student")
	passport := passport{SchemaVersion: 2, CurriculumVersion: "1.2.0", GitHubUser: "student", SourceRepository: sourceRepository, ForkRepository: "student/passport-exercises", AssessmentBranch: "onboarding/student", Platform: "macos", Responsibilities: []string{}, Missions: []string{"core-orientation", "euler-access-ssh"}, SetupComplete: true}
	raw, _ = json.Marshal(passport) // Preserve these original bytes, not launcher formatting.
	write("passport.json", raw)
	write("published-work.txt", []byte("synthetic first revision\n"))
	resumeGit(t, seed, "add", ".")
	resumeGit(t, seed, "commit", "-m", "test: existing Mac Passport")
	fork := filepath.Join(base, "fork.git")
	resumeGit(t, base, "clone", "--bare", seed, fork)
	return &resumeRunner{fork: fork, upstream: upstream, recordState: "open"}, base, raw
}

func TestResumeRoundTripPreservesRouteDraftsAndPublishedWork(t *testing.T) {
	r, base, original := resumeFixture(t)
	resume := func(computer string) string {
		t.Helper()
		dir := filepath.Join(base, computer, ".transport")
		result, err := run(Options{ResumeOnly: true, Directory: dir, Platform: "linux", Output: &strings.Builder{}}, r)
		if err != nil {
			t.Fatal(err)
		}
		if result.Directory != dir || result.PullRequestURL == "" {
			t.Fatalf("bad result: %#v", result)
		}
		raw, _ := os.ReadFile(filepath.Join(dir, "passport.json"))
		if !bytes.Equal(raw, original) {
			t.Fatal("resume rewrote protected route")
		}
		return dir
	}
	mac := resume("mac")
	linux := resume("linux")
	if err := os.MkdirAll(filepath.Join(mac, ".passport-local"), 0o700); err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(mac, ".passport-local", "state.json")
	if err := os.WriteFile(draft, []byte("synthetic local draft"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Simulate a later, explicitly submitted revision from the second computer.
	if err := os.WriteFile(filepath.Join(linux, "published-work.txt"), []byte("synthetic second revision\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resumeGit(t, linux, "add", "published-work.txt")
	resumeGit(t, linux, "commit", "-m", "test: simulated Linux submission")
	resumeGit(t, linux, "push", r.fork, "HEAD:onboarding/student")
	head := resumeGit(t, linux, "rev-parse", "HEAD")
	resume("mac")
	if got := resumeGit(t, mac, "rev-parse", "HEAD"); got != head {
		t.Fatal("Mac did not receive the published revision")
	}
	if raw, _ := os.ReadFile(draft); string(raw) != "synthetic local draft" {
		t.Fatal("Mac draft was changed")
	}
	if _, err := os.Stat(filepath.Join(linux, ".passport-local", "state.json")); !os.IsNotExist(err) {
		t.Fatal("local draft was silently copied")
	}
	r.recordState = "closed"
	resume("third") // Completed records can be reopened, never recreated.
	for _, call := range r.calls {
		for _, forbidden := range []string{"gh repo fork", "gh pr create", "git push", "git commit", "git reset", "git clean"} {
			if strings.HasPrefix(call, forbidden) {
				t.Fatalf("resume mutated history or GitHub: %s", call)
			}
		}
	}
}

func TestResumeRefusesConflictsAndRecoversAnInterruptedFetch(t *testing.T) {
	r, base, original := resumeFixture(t)
	dir := filepath.Join(base, "returning", ".transport")
	options := Options{ResumeOnly: true, Directory: dir, Output: &strings.Builder{}}
	r.fail = "git fetch --prune origin"
	if _, err := run(options, r); err == nil {
		t.Fatal("interrupted fetch succeeded")
	}
	r.fail = ""
	if _, err := run(options, r); err != nil {
		t.Fatal(err)
	}
	before := resumeGit(t, dir, "rev-parse", "HEAD")
	path := filepath.Join(dir, "passport.json")
	if err := os.WriteFile(path, append(original, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(options, r); err == nil {
		t.Fatal("dirty protected file was accepted")
	}
	if raw, _ := os.ReadFile(path); !bytes.Equal(raw, append(original, '\n')) {
		t.Fatal("dirty work was overwritten")
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "local.txt"), []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	resumeGit(t, dir, "add", "local.txt")
	resumeGit(t, dir, "commit", "-m", "test: local unsent work")
	localHead := resumeGit(t, dir, "rev-parse", "HEAD")
	seed := filepath.Join(base, "seed")
	if err := os.WriteFile(filepath.Join(seed, "remote.txt"), []byte("other computer"), 0o600); err != nil {
		t.Fatal(err)
	}
	resumeGit(t, seed, "add", "remote.txt")
	resumeGit(t, seed, "commit", "-m", "test: independent remote work")
	resumeGit(t, seed, "push", r.fork, "HEAD:onboarding/student")
	if _, err := run(options, r); err == nil {
		t.Fatal("divergent histories were accepted")
	}
	if resumeGit(t, dir, "rev-parse", "HEAD") != localHead || localHead == before {
		t.Fatal("local commit changed")
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "local.txt")); string(raw) != "keep me" {
		t.Fatal("local file changed")
	}
}

func TestResumeMissingAccountOrRecordCreatesNothing(t *testing.T) {
	for _, failure := range []string{"gh api repos/student/", "/pulls?state=all"} {
		r, base, _ := resumeFixture(t)
		r.fail = failure
		dir := filepath.Join(base, "new", ".transport")
		if _, err := run(Options{ResumeOnly: true, Directory: dir, Output: &strings.Builder{}}, r); err == nil {
			t.Fatal("unavailable record was accepted")
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("created a replacement local record")
		}
		for _, call := range r.calls {
			if strings.Contains(call, "clone") || strings.Contains(call, "fork ") || strings.Contains(call, "pr create") {
				t.Fatalf("unexpected creation: %s", call)
			}
		}
	}
}

func TestResumeRefusesMissingOrAmbiguousRecords(t *testing.T) {
	for _, response := range []string{
		`[]`, `not json`,
		`[{"state":"open","html_url":"https://github.com/soheylm-passport-sandbox/passport-exercises/pull/1"},{"state":"open","html_url":"https://github.com/soheylm-passport-sandbox/passport-exercises/pull/2"}]`,
		`[{"state":"closed","html_url":"https://github.com/soheylm-passport-sandbox/passport-exercises/pull/1","merged_at":"2026-09-21T12:00:00Z"}]`,
	} {
		r := &resumeRunner{recordResponse: response}
		if _, err := existingLearningRecord(context.Background(), r, "student"); err == nil {
			t.Fatalf("invalid record accepted: %s", response)
		}
	}
}

func TestResumeDoesNotTreatParentRepositoryAsThePassport(t *testing.T) {
	r, base, _ := resumeFixture(t)
	directory := filepath.Join(base, "ordinary-folder")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := cloneExistingPassport(context.Background(), r, "student", directory); err == nil {
		t.Fatal("accepted a folder without its own Git metadata")
	}
	if len(r.calls) != 0 {
		t.Fatal("ran commands in an unrelated folder")
	}
}
