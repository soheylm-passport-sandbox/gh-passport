package starter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type branchRunner struct {
	local    bool
	remote   bool
	commands []string
}

type missingGitRunner struct {
	commands []string
}

func (runner *missingGitRunner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	runner.commands = append(runner.commands, strings.Join(append([]string{name}, args...), " "))
	return nil, errors.New("executable not found")
}

func TestStartStopsBeforeGitHubChangesWhenGitIsMissing(t *testing.T) {
	runner := &missingGitRunner{}
	_, err := run(Options{Input: strings.NewReader(""), Output: &strings.Builder{}}, runner)
	if err == nil || !strings.Contains(err.Error(), "install Git") {
		t.Fatalf("missing Git did not produce a safe prerequisite error: %v", err)
	}
	if expected := []string{"git --version"}; !reflect.DeepEqual(runner.commands, expected) {
		t.Fatalf("commands = %#v; want %#v", runner.commands, expected)
	}
}

func (runner *branchRunner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	command := strings.Join(append([]string{name}, args...), " ")
	runner.commands = append(runner.commands, command)
	if strings.Contains(command, "refs/heads/onboarding/student") && !strings.Contains(command, "refs/remotes/") {
		if !runner.local {
			return nil, errors.New("missing local branch")
		}
	}
	if strings.Contains(command, "refs/remotes/origin/onboarding/student") && !runner.remote {
		return nil, errors.New("missing remote branch")
	}
	return nil, nil
}

func TestPrepareBranchTracksRemoteRouteOnANewComputer(t *testing.T) {
	runner := &branchRunner{remote: true}
	if err := prepareBranch(context.Background(), runner, ".", "onboarding/student"); err != nil {
		t.Fatal(err)
	}
	commands := strings.Join(runner.commands, "\n")
	if !strings.Contains(commands, "git switch --create onboarding/student --track origin/onboarding/student") {
		t.Fatalf("remote route was not resumed:\n%s", commands)
	}
	if strings.Contains(commands, "upstream/main") {
		t.Fatalf("remote route was replaced from upstream main:\n%s", commands)
	}
}

func TestPrepareBranchFastForwardsAnExistingCleanRoute(t *testing.T) {
	runner := &branchRunner{local: true, remote: true}
	if err := prepareBranch(context.Background(), runner, ".", "onboarding/student"); err != nil {
		t.Fatal(err)
	}
	commands := strings.Join(runner.commands, "\n")
	if !strings.Contains(commands, "git merge --ff-only origin/onboarding/student") {
		t.Fatalf("existing route was not safely refreshed:\n%s", commands)
	}
}

type forkRunner struct {
	value []byte
	err   error
}

func (runner forkRunner) Run(_ context.Context, _ string, _ string, _ ...string) ([]byte, error) {
	return runner.value, runner.err
}

func TestVerifyDirectForkRefusesASameNameUnrelatedRepository(t *testing.T) {
	if err := verifyDirectFork(
		context.Background(),
		forkRunner{value: []byte("true\tsoheylm-passport-sandbox/passport-exercises\n")},
		"student",
	); err != nil {
		t.Fatalf("official direct fork was rejected: %v", err)
	}
	for _, value := range []string{
		"false\t\n",
		"true\tattacker/passport-exercises\n",
		"true\tsoheylm-passport-sandbox/passport-exercises-extra\n",
	} {
		if err := verifyDirectFork(
			context.Background(), forkRunner{value: []byte(value)}, "student",
		); err == nil {
			t.Fatalf("unrelated repository metadata %q was accepted", value)
		}
	}
}

type pullRequestRunner struct {
	lookups   [][]byte
	createErr error
	commands  []string
}

func (runner *pullRequestRunner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	command := strings.Join(append([]string{name}, args...), " ")
	runner.commands = append(runner.commands, command)
	if name == "gh" && len(args) > 0 && args[0] == "api" {
		if len(runner.lookups) == 0 {
			return nil, nil
		}
		value := runner.lookups[0]
		runner.lookups = runner.lookups[1:]
		return value, nil
	}
	if name == "gh" && len(args) > 1 && args[0] == "pr" && args[1] == "create" {
		return nil, runner.createErr
	}
	return nil, errors.New("unexpected command")
}

func TestEnsurePullRequestReusesThePermanentForkPR(t *testing.T) {
	const expected = "https://github.com/soheylm-passport-sandbox/passport-exercises/pull/7"
	runner := &pullRequestRunner{lookups: [][]byte{[]byte(expected + "\n")}}
	actual, err := ensurePullRequest(
		context.Background(), runner, "Student", "onboarding/student", ".",
	)
	if err != nil || actual != expected {
		t.Fatalf("existing PR = %q, %v; want %q", actual, err, expected)
	}
	commands := strings.Join(runner.commands, "\n")
	if !strings.Contains(commands, "head=Student%3Aonboarding%2Fstudent") {
		t.Fatalf("fork owner and branch were not encoded in the API query:\n%s", commands)
	}
	if strings.Contains(commands, "gh pr create") {
		t.Fatalf("existing permanent PR was recreated:\n%s", commands)
	}
}

func TestEnsurePullRequestRecoversFromAConcurrentCreate(t *testing.T) {
	const expected = "https://github.com/soheylm-passport-sandbox/passport-exercises/pull/7"
	runner := &pullRequestRunner{
		lookups:   [][]byte{nil, []byte(expected + "\n")},
		createErr: errors.New("pull request already exists"),
	}
	actual, err := ensurePullRequest(
		context.Background(), runner, "Student", "onboarding/student", ".",
	)
	if err != nil || actual != expected {
		t.Fatalf("concurrent PR = %q, %v; want %q", actual, err, expected)
	}
}

func TestGitHubRepositoryAcceptsOnlyExactGitHubRepositoryURLs(t *testing.T) {
	accepted := map[string]string{
		"https://github.com/student/passport-exercises.git": "student/passport-exercises",
		"git@github.com:student/passport-exercises.git":     "student/passport-exercises",
		"ssh://git@github.com/student/passport-exercises":   "student/passport-exercises",
	}
	for raw, expected := range accepted {
		actual, ok := githubRepository(raw)
		if !ok || actual != expected {
			t.Fatalf("%q parsed as %q, %v; want %q", raw, actual, ok, expected)
		}
	}
	for _, raw := range []string{
		"https://evil.example/student/passport-exercises.git",
		"https://github.com/attacker/student/passport-exercises.git",
		"https://github.com/student/passport-exercises.git?expected=student/passport-exercises",
		"file:///tmp/student/passport-exercises",
	} {
		if actual, ok := githubRepository(raw); ok {
			t.Fatalf("unsafe remote %q parsed as %q", raw, actual)
		}
	}
}

func TestResolveMissionsAddsUniversalAndTransitiveTracksInCatalogOrder(t *testing.T) {
	value := catalog{
		UniversalTracks: []string{"core"},
		TrackDependencies: map[string][]string{
			"core": {}, "git": {"core"}, "euler": {"git"},
		},
		Responsibilities: map[string]responsibility{
			"compute": {Tracks: []string{"euler"}},
		},
		Tracks: []track{
			{ID: "core", Missions: []string{"safe"}},
			{ID: "git", Missions: []string{"commit"}},
			{ID: "euler", Missions: []string{"job"}},
		},
	}
	actual, err := resolveMissions(value, []string{"compute"})
	if err != nil {
		t.Fatal(err)
	}
	if expected := []string{"safe", "commit", "job"}; !reflect.DeepEqual(actual, expected) {
		t.Fatalf("missions = %#v; want %#v", actual, expected)
	}
}

func TestDecodeCatalogAcceptsAdditiveTeachingMetadata(t *testing.T) {
	raw := []byte(`{
  "schema_version": 2,
  "curriculum_version": "2.1.1",
  "source_repository": "soheylm-passport-sandbox/passport-exercises",
  "platforms": ["windows", "macos", "linux"],
  "universal_tracks": ["core"],
  "track_dependencies": {"core": []},
  "responsibilities": {
    "git": {
      "title": "I edit code or documentation",
      "description": "Learn Git and GitHub before using them for assessed work.",
      "tracks": ["core"],
      "future_display_hint": "safe additive metadata"
    }
  },
  "tracks": [{"id": "core", "title": "Safety", "missions": ["safe"]}],
  "missions": {
    "safe": {
      "track": "core",
      "title": "Work safely",
      "activity": {},
      "verification": {},
      "submission": {},
      "review_policy": "automatic"
    }
  },
  "future_catalog_metadata": {"purpose": "teaching"}
}`)

	value, err := decodeCatalog(raw)
	if err != nil {
		t.Fatalf("additive teaching metadata broke the launcher: %v", err)
	}
	if value.Responsibilities["git"].Description == "" {
		t.Fatal("responsibility description was not decoded")
	}
}

func TestWritePassportIsIdempotentAndRefusesRouteReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "passport.json")
	value := passport{
		SchemaVersion: 2, CurriculumVersion: "1.2.0", GitHubUser: "student",
		SourceRepository: sourceRepository, ForkRepository: "student/passport-exercises",
		AssessmentBranch: "onboarding/student", Platform: "linux",
		Missions: []string{"safe"},
	}
	changed, err := writePassport(path, value)
	if err != nil || !changed {
		t.Fatalf("first write = %v, %v", changed, err)
	}
	changed, err = writePassport(path, value)
	if err != nil || changed {
		t.Fatalf("idempotent write = %v, %v", changed, err)
	}
	value.Platform = "windows"
	if _, err := writePassport(path, value); err == nil || !strings.Contains(err.Error(), "do not overwrite") {
		t.Fatalf("route replacement was not refused: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), `"platform": "linux"`) {
		t.Fatalf("original passport was not preserved: %v, %s", err, raw)
	}
}

func TestInterruptedGeneratedPassportCanResumeWithoutAcceptingOtherChanges(t *testing.T) {
	for _, status := range []string{
		"?? passport.json\n",
		"A  passport.json\n",
		" M passport.json\n",
		"MM passport.json\n",
	} {
		if !onlyGeneratedPassportChange(status) {
			t.Fatalf("safe interrupted status was rejected: %q", status)
		}
	}
	for _, status := range []string{
		"?? notes.txt\n",
		"?? passport.json\n?? notes.txt\n",
		"R  passport.json -> stolen.json\n",
		" M passport.json/child\n",
		"",
	} {
		if onlyGeneratedPassportChange(status) {
			t.Fatalf("unrelated or malformed status was accepted: %q", status)
		}
	}
}

func TestLoadExistingPassportReusesOnlyTheCanonicalOwnedRoute(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "passport.json")
	value := catalog{
		CurriculumVersion: "1.2.0", UniversalTracks: []string{"core"},
		TrackDependencies: map[string][]string{"core": {}},
		Responsibilities:  map[string]responsibility{},
		Tracks:            []track{{ID: "core", Missions: []string{"safe"}}},
	}
	existing := passport{
		SchemaVersion: 2, CurriculumVersion: "1.2.0", GitHubUser: "Student",
		SourceRepository: sourceRepository, ForkRepository: "Student/passport-exercises",
		AssessmentBranch: "onboarding/student", Platform: "linux", Missions: []string{"safe"},
	}
	if _, err := writePassport(path, existing); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadExistingPassport(path, value, "Student", "onboarding/student")
	if err != nil || loaded == nil || loaded.Platform != "linux" {
		t.Fatalf("canonical route was not reused: %#v, %v", loaded, err)
	}
	existing.Missions = []string{"invented"}
	raw, _ := json.Marshal(existing)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadExistingPassport(path, value, "Student", "onboarding/student"); err == nil {
		t.Fatal("non-canonical route was reused")
	}
}
