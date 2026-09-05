package localserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/githubstatus"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/launcherupdate"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/passportrepo"
)

type ignoredRunner struct{}

func (ignoredRunner) Run(_ context.Context, _ string, _ string, _ ...string) ([]byte, error) {
	return []byte{}, nil
}

type unavailableGitHub struct{}

type fakeUpdater struct {
	status   launcherupdate.Status
	prepared launcherupdate.Prepared
	err      error
}

func (updater fakeUpdater) Check(context.Context) launcherupdate.Status {
	return updater.status
}

func (updater fakeUpdater) Prepare(context.Context) (launcherupdate.Prepared, error) {
	return updater.prepared, updater.err
}

func (unavailableGitHub) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return nil, context.DeadlineExceeded
}

func TestLauncherUpdateIsServerSelectedAndQueued(t *testing.T) {
	ready := make(chan launcherupdate.Prepared, 1)
	prepared := launcherupdate.Prepared{Version: "v0.4.3", HelperPath: "helper", PlanPath: "plan", LogPath: "log"}
	server := &Server{}
	server.EnableUpdates(fakeUpdater{
		status:   launcherupdate.Status{State: "available", CurrentVersion: "v0.4.2", CurriculumVersion: "2.1.1"},
		prepared: prepared,
	}, ready)

	statusRecorder := httptest.NewRecorder()
	server.updateStatus(statusRecorder, httptest.NewRequest(http.MethodGet, "/__passport/v1/update", nil))
	if statusRecorder.Code != http.StatusOK || !strings.Contains(statusRecorder.Body.String(), `"state":"available"`) {
		t.Fatalf("update status = %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}

	startRecorder := httptest.NewRecorder()
	server.startUpdate(startRecorder, httptest.NewRequest(http.MethodPost, "/__passport/v1/update", nil))
	if startRecorder.Code != http.StatusAccepted || !strings.Contains(startRecorder.Body.String(), `"version":"v0.4.3"`) {
		t.Fatalf("start update = %d: %s", startRecorder.Code, startRecorder.Body.String())
	}
	select {
	case queued := <-ready:
		if queued != prepared {
			t.Fatalf("queued update = %#v", queued)
		}
	case <-time.After(time.Second):
		t.Fatal("verified update was not queued")
	}
	secondRecorder := httptest.NewRecorder()
	server.startUpdate(secondRecorder, httptest.NewRequest(http.MethodPost, "/__passport/v1/update", nil))
	if secondRecorder.Code != http.StatusConflict {
		t.Fatalf("second update status = %d: %s", secondRecorder.Code, secondRecorder.Body.String())
	}
}

func TestLauncherUpdateFailureDoesNotQueueOrExposeDetails(t *testing.T) {
	ready := make(chan launcherupdate.Prepared, 1)
	server := &Server{}
	server.EnableUpdates(fakeUpdater{err: errors.New("token or private path must not reach browser")}, ready)
	recorder := httptest.NewRecorder()
	server.startUpdate(recorder, httptest.NewRequest(http.MethodPost, "/__passport/v1/update", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "token") || strings.Contains(recorder.Body.String(), "private path") {
		t.Fatalf("private update failure leaked to browser: %s", recorder.Body.String())
	}
	select {
	case <-ready:
		t.Fatal("failed update was queued")
	default:
	}
}

type gitEnvironmentRunner struct {
	root  string
	name  string
	email string
}

type agentProjectRunner struct {
	changed string
}

type pythonProjectRunner struct {
	changed   string
	testsPass bool
}

type aiConfigurationRunner struct {
	root   string
	status string
	remote string
}

type sshConfigRunner struct {
	output string
	err    error
}

type practiceRunner struct {
	root   string
	origin string
	calls  []string
}

func (runner *practiceRunner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	command := strings.Join(append([]string{name}, args...), " ")
	runner.calls = append(runner.calls, command)
	switch command {
	case "git clone " + runner.origin + " " + runner.root:
		if err := os.MkdirAll(runner.root, 0o700); err != nil {
			return nil, err
		}
		return nil, nil
	case "git remote get-url origin":
		return []byte(runner.origin + "\n"), nil
	case "git fetch --prune origin":
		return nil, nil
	case "git show-ref --verify --quiet refs/heads/practice/student":
		return nil, errors.New("local branch absent")
	case "git show-ref --verify --quiet refs/remotes/origin/practice/student":
		return nil, errors.New("remote branch absent")
	case "git switch --create practice/student origin/main":
		return nil, nil
	default:
		return nil, errors.New("unexpected command: " + command)
	}
}

func (runner sshConfigRunner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	if name == "ssh" && strings.Join(args, " ") == "-G euler" {
		return []byte(runner.output), runner.err
	}
	return nil, errors.New("unexpected command")
}

func (runner agentProjectRunner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	if strings.Join(append([]string{name}, args...), " ") == "git diff --name-only HEAD -- workspace/agent_task" {
		return []byte(runner.changed), nil
	}
	return nil, errors.New("unexpected command")
}

func (runner pythonProjectRunner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	command := strings.Join(args, " ")
	if filepath.Base(name) == "python" && command == "-m unittest discover -s tests -v" {
		if runner.testsPass {
			return []byte("OK\n"), nil
		}
		return nil, errors.New("tests failed")
	}
	if name == "git" && command == "diff --name-only HEAD -- workspace/python_project" {
		return []byte(runner.changed), nil
	}
	return nil, errors.New("unexpected command: " + name + " " + command)
}

func (runner aiConfigurationRunner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	command := strings.Join(append([]string{name}, args...), " ")
	switch command {
	case "git rev-parse --show-toplevel":
		return []byte(runner.root + "\n"), nil
	case "git branch --show-current":
		return []byte("practice/student\n"), nil
	case "git remote -v":
		return []byte(runner.remote), nil
	case "git status --porcelain=v1 --untracked-files=all -- workspace/agent_task .vscode .zed .env .env.local":
		return []byte(runner.status), nil
	default:
		return nil, errors.New("unexpected command: " + command)
	}
}

func (runner gitEnvironmentRunner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	command := strings.Join(append([]string{name}, args...), " ")
	switch command {
	case "git --version":
		return []byte("git version 2.50.0\n"), nil
	case "gh api user --jq .login":
		return []byte("student\n"), nil
	case "git config --get user.name":
		return []byte(runner.name + "\n"), nil
	case "git config --get user.email":
		return []byte(runner.email + "\n"), nil
	case "git remote -v":
		return []byte("origin\thttps://github.com/student/passport-exercises.git (fetch)\n"), nil
	case "git branch --show-current":
		return []byte("practice/student\n"), nil
	case "git rev-parse --show-toplevel":
		return []byte(runner.root + "\n"), nil
	default:
		return nil, errors.New("unexpected command: " + command)
	}
}

func TestGitEnvironmentUsesPracticeAndRejectsManagedTransportIdentity(t *testing.T) {
	parent := t.TempDir()
	transport := filepath.Join(parent, ".transport")
	practice := filepath.Join(parent, "practice")
	server := &Server{repository: passportrepo.Repository{
		Root:     transport,
		Passport: passportrepo.Passport{GitHubUser: "student"},
	}}
	server.runner = gitEnvironmentRunner{root: practice, name: "Student Name", email: "student@example.org"}
	checks := server.verifyGitEnvironment()
	for name, passed := range checks {
		if !passed {
			t.Fatalf("learner practice check %s failed: %#v", name, checks)
		}
	}
	server.runner = gitEnvironmentRunner{root: practice, name: passportrepo.ManagedGitName, email: passportrepo.ManagedGitEmail}
	checks = server.verifyGitEnvironment()
	if checks["identity_name"] || checks["identity_email"] {
		t.Fatalf("managed transport identity satisfied the Git mission: %#v", checks)
	}
	server.repository.Passport.GitHubUser = "another-student"
	server.runner = gitEnvironmentRunner{root: practice, name: "Student Name", email: "student@example.org"}
	if checks = server.verifyGitEnvironment(); checks["github_auth"] {
		t.Fatalf("wrong active GitHub account satisfied the Git mission: %#v", checks)
	}
}

func TestPreparePracticeCreatesASeparateFolderAndBranch(t *testing.T) {
	parent := t.TempDir()
	transport := filepath.Join(parent, ".transport")
	root := filepath.Join(parent, "practice")
	origin := "https://github.com/student/passport-exercises.git"
	runner := &practiceRunner{root: root, origin: origin}
	server := &Server{
		repository: passportrepo.Repository{
			Root:  transport,
			Owner: "student",
			Name:  "passport-exercises",
			Passport: passportrepo.Passport{
				GitHubUser: "Student",
			},
		},
		runner: runner,
	}
	recorder := httptest.NewRecorder()
	server.preparePractice(recorder, httptest.NewRequest(http.MethodPost, "/__passport/v2/practice", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("prepare practice status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["path"] != root || response["branch"] != "practice/student" || response["status"] != "ready" {
		t.Fatalf("unexpected practice response: %#v", response)
	}
	if !strings.Contains(strings.Join(runner.calls, "\n"), "git switch --create practice/student origin/main") {
		t.Fatalf("practice branch was not created from origin/main: %#v", runner.calls)
	}
}

func TestPythonEnvironmentRequiresIgnoredCondaPrefixAndItsInterpreter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture uses a POSIX executable")
	}
	parent := t.TempDir()
	practice := filepath.Join(parent, "practice")
	python := filepath.Join(practice, ".venv", "bin", "python")
	pythonTarget := filepath.Join(practice, ".venv", "bin", "python3.11")
	history := filepath.Join(practice, ".venv", "conda-meta", "history")
	for _, directory := range []string{filepath.Dir(python), filepath.Dir(history)} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	interpreterProbe := "#!/bin/sh\ncase \"$*\" in *\"sys.version_info[:2] == (3, 11)\"*) exit 0;; *) exit 1;; esac\n"
	if err := os.WriteFile(pythonTarget, []byte(interpreterProbe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("python3.11", python); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(history, []byte("# conda environment history\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(practice, ".gitignore"), []byte(".venv/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(practice, "environment.yml"), []byte("channels:\n  - conda-forge\n  - nodefaults\ndependencies:\n  - python=3.11\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "init", "--quiet", practice)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	command = exec.Command("git", "-C", practice, "add", "environment.yml")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git add environment definition: %v: %s", err, output)
	}
	command = exec.Command(
		"git", "-C", practice,
		"-c", "user.name=Passport Test",
		"-c", "user.email=passport@example.invalid",
		"commit", "--quiet", "-m", "test: add environment definition",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git commit environment definition: %v: %s", err, output)
	}
	server := &Server{
		repository: passportrepo.Repository{Root: filepath.Join(parent, ".transport")},
		runner:     passportrepo.ExecRunner{},
	}
	checks := server.verifyPythonEnvironment()
	for name, passed := range checks {
		if !passed {
			t.Fatalf("valid Conda environment check %s failed: %#v", name, checks)
		}
	}
	if err := os.WriteFile(filepath.Join(practice, "environment.yml"), []byte("channels:\n  - defaults\ndependencies:\n  - python=3.12\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if checks = server.verifyPythonEnvironment(); checks["environment_definition"] {
		t.Fatalf("changed environment definition passed: %#v", checks)
	}
	if err := os.WriteFile(filepath.Join(practice, "environment.yml"), []byte("channels:\n  - conda-forge\n  - nodefaults\ndependencies:\n  - python=3.11\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(history); err != nil {
		t.Fatal(err)
	}
	checks = server.verifyPythonEnvironment()
	if checks["conda_environment"] {
		t.Fatalf("environment without conda-meta/history passed: %#v", checks)
	}
	if err := os.Rename(filepath.Join(practice, ".venv"), filepath.Join(practice, ".venv-real")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".venv-real", filepath.Join(practice, ".venv")); err != nil {
		t.Fatal(err)
	}
	checks = server.verifyPythonEnvironment()
	if checks["venv_exists"] || checks["venv_interpreter"] {
		t.Fatalf("symbolic-link environment passed: %#v", checks)
	}
}

func TestPythonProjectVerifierMatchesTheTaughtChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture uses a POSIX environment path")
	}
	parent := t.TempDir()
	practice := filepath.Join(parent, "practice")
	project := filepath.Join(practice, "workspace", "python_project")
	if err := os.MkdirAll(filepath.Join(project, "tests"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := "def total_memory_gib(cpus: int, memory_per_cpu_gib: int) -> int:\n    if cpus < 1 or memory_per_cpu_gib < 1:\n        raise ValueError('positive')\n    return cpus * memory_per_cpu_gib\n"
	testSource := "from passport_example import total_memory_gib\n\ndef test_multiple_cpus():\n    self.assertEqual(total_memory_gib(4, 3), 12)\n"
	if err := os.WriteFile(filepath.Join(project, "passport_example.py"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "tests", "test_passport_example.py"), []byte(testSource), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{repository: passportrepo.Repository{Root: filepath.Join(parent, ".transport")}}
	server.runner = pythonProjectRunner{
		testsPass: true,
		changed: "workspace/python_project/tests/test_passport_example.py\n" +
			"workspace/python_project/passport_example.py\n",
	}
	checks := server.verifyPythonProject()
	for name, passed := range checks {
		if !passed {
			t.Fatalf("valid Python project check %s failed: %#v", name, checks)
		}
	}
	server.runner = pythonProjectRunner{testsPass: true, changed: "workspace/python_project/passport_example.py\nnotes.txt\n"}
	checks = server.verifyPythonProject()
	if checks["bounded_diff"] {
		t.Fatalf("extra changed path passed: %#v", checks)
	}
}

func TestAIConfigurationVerifierScopesCleanlinessToAgentPaths(t *testing.T) {
	parent := t.TempDir()
	practice := filepath.Join(parent, "practice")
	server := &Server{
		repository: passportrepo.Repository{
			Root:     filepath.Join(parent, ".transport"),
			Passport: passportrepo.Passport{GitHubUser: "student"},
		},
	}
	server.runner = aiConfigurationRunner{root: practice, remote: "origin\thttps://github.com/student/passport-exercises.git (fetch)\n"}
	checks := server.verifyAIConfiguration()
	for name, passed := range checks {
		if !passed {
			t.Fatalf("valid AI configuration check %s failed: %#v", name, checks)
		}
	}
	server.runner = aiConfigurationRunner{root: practice, status: "?? .env\n", remote: "origin\thttps://github.com/student/passport-exercises.git (fetch)\n"}
	checks = server.verifyAIConfiguration()
	if checks["practice_paths_clean"] {
		t.Fatalf("credential path passed scoped clean check: %#v", checks)
	}
}

func TestSSHConfigVerifierKeepsAWorkingPrivateIdentityAndDisablesFallback(t *testing.T) {
	valid := "hostname euler.ethz.ch\nport 22\nuser student\nidentityfile /home/student/.ssh/id_ed25519_euler\nidentitiesonly yes\npreferredauthentications publickey\npasswordauthentication no\nkbdinteractiveauthentication no\nforwardagent no\n"
	server := &Server{repository: passportrepo.Repository{Root: t.TempDir()}, runner: sshConfigRunner{output: valid}}
	checks := server.verifySSHConfig()
	for name, passed := range checks {
		if !passed {
			t.Fatalf("valid SSH config check %s failed: %#v", name, checks)
		}
	}

	unsafe := strings.Replace(valid, "passwordauthentication no", "passwordauthentication yes", 1)
	server.runner = sshConfigRunner{output: unsafe}
	checks = server.verifySSHConfig()
	if checks["password_disabled"] {
		t.Fatalf("password fallback passed: %#v", checks)
	}

	proxied := valid + "proxycommand ssh stale-tunnel connect\n"
	server.runner = sshConfigRunner{output: proxied}
	checks = server.verifySSHConfig()
	if checks["direct_connection"] {
		t.Fatalf("stale proxy command passed: %#v", checks)
	}

	existing := strings.Replace(valid, "id_ed25519_euler\nidentitiesonly yes", "existing_euler_key\nidentitiesonly no", 1)
	server.runner = sshConfigRunner{output: existing}
	checks = server.verifySSHConfig()
	for name, passed := range checks {
		if !passed {
			t.Fatalf("working existing identity check %s failed: %#v", name, checks)
		}
	}

	publicKey := strings.Replace(valid, "id_ed25519_euler\n", "id_ed25519_euler.pub\n", 1)
	server.runner = sshConfigRunner{output: publicKey}
	checks = server.verifySSHConfig()
	if checks["private_identity_selection"] {
		t.Fatalf("public key accepted as IdentityFile: %#v", checks)
	}

	noIdentity := strings.Replace(valid, "/home/student/.ssh/id_ed25519_euler", "none", 1)
	server.runner = sshConfigRunner{output: noIdentity}
	checks = server.verifySSHConfig()
	if checks["private_identity_selection"] {
		t.Fatalf("IdentityFile none passed: %#v", checks)
	}
}

func TestSlurmArrayVerifierRequiresExactLimitAndTwoUniqueLogs(t *testing.T) {
	parent := t.TempDir()
	directory := filepath.Join(parent, "practice", "workspace", "slurm")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "array_job.slurm.txt")
	valid := "#!/bin/bash\n#SBATCH --account=es_fuge\n#SBATCH --array=0-9%1\n#SBATCH --time=00:10:00\n#SBATCH --cpus-per-task=1\n#SBATCH --mem-per-cpu=1G\n#SBATCH --output=logs/%x_%A_%a.out\n#SBATCH --error=logs/%x_%A_%a.err\n"
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{repository: passportrepo.Repository{Root: filepath.Join(parent, ".transport")}}
	checks := server.verifySlurmArray()
	for name, passed := range checks {
		if !passed {
			t.Fatalf("valid Slurm array check %s failed: %#v", name, checks)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Replace(valid, "%x_%A_%a.err", "%x_%j.err", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if server.verifySlurmArray()["error_unique"] {
		t.Fatal("non-unique array error log passed")
	}
}

func TestGPUVerifierAcceptsDocumentedModelsAndRejectsExtraMemory(t *testing.T) {
	parent := t.TempDir()
	directory := filepath.Join(parent, "practice", "workspace", "slurm")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "gpu_job.slurm.txt")
	server := &Server{repository: passportrepo.Repository{Root: filepath.Join(parent, ".transport")}}
	for _, model := range []string{"rtx_4090", "rtx_3090", "pro_6000"} {
		valid := "#!/bin/bash\n#SBATCH --account=es_fuge\n#SBATCH --gpus=" + model + ":1\n#SBATCH --cpus-per-task=4\n#SBATCH --mem-per-cpu=3G\n#SBATCH --time=00:30:00\n#SBATCH --output=logs/%x_%j.out\n#SBATCH --error=logs/%x_%j.err\n"
		if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
			t.Fatal(err)
		}
		checks := server.verifyGPUScript()
		for name, passed := range checks {
			if !passed {
				t.Fatalf("valid %s GPU check %s failed: %#v", model, name, checks)
			}
		}
		if err := os.WriteFile(path, []byte(valid+"#SBATCH --mem=12G\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if server.verifyGPUScript()["memory_limit"] {
			t.Fatalf("%s GPU script with two memory modes passed", model)
		}
	}
}

func TestHandoverVerifierRequiresFutureDeletionAndKnownLimitation(t *testing.T) {
	parent := t.TempDir()
	directory := filepath.Join(parent, "practice", "workspace", "handover")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "project-handover.md")
	future := time.Now().UTC().AddDate(0, 0, 30).Format("2006-01-02")
	valid := "Current owner: Student One\nAuthorized successor: Student Two\nRevision: " + strings.Repeat("a", 40) + "\nAuthoritative location: P:/Supervisor/student\nTemporary locations to remove: D:/student/tmp\nEnvironment definition: environment.yml\nVerification command: python -m unittest\nExpected result: OK\nAccess owner: Supervisor\nRetention owner: Supervisor\nTemporary-copy deletion date: " + future + "\nUnresolved risk or limitation: One synthetic limitation.\n"
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{repository: passportrepo.Repository{Root: filepath.Join(parent, ".transport")}}
	checks := server.verifyHandover()
	for name, passed := range checks {
		if !passed {
			t.Fatalf("valid handover check %s failed: %#v", name, checks)
		}
	}
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	invalid := strings.Replace(valid, future, yesterday, 1)
	invalid = strings.Replace(invalid, "Unresolved risk or limitation: One synthetic limitation.\n", "", 1)
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	checks = server.verifyHandover()
	if checks["retention"] || checks["known_limitations"] {
		t.Fatalf("invalid handover passed: %#v", checks)
	}
}

func TestAgentVerifierEnforcesBoundedDiffAndCanary(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "practice", "workspace", "agent_task")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	plan := "P: is durable. D: is temporary. C: is not project storage. Heavy work runs on Euler.\n"
	if err := os.WriteFile(filepath.Join(root, "storage-plan.md"), []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scope-canary.txt"), []byte("IDEAL-PASSPORT-AGENT-SCOPE-CANARY-v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{repository: passportrepo.Repository{Root: filepath.Join(parent, ".transport")}}
	server.runner = agentProjectRunner{changed: "workspace/agent_task/storage-plan.md\n"}
	checks := server.verifyAgentProject()
	for name, passed := range checks {
		if !passed {
			t.Fatalf("safe agent project check %s failed: %#v", name, checks)
		}
	}
	server.runner = agentProjectRunner{changed: "workspace/agent_task/storage-plan.md\nworkspace/agent_task/scope-canary.txt\n"}
	checks = server.verifyAgentProject()
	if checks["bounded_diff"] {
		t.Fatalf("extra changed file passed bounded diff: %#v", checks)
	}
	if err := os.WriteFile(filepath.Join(root, "scope-canary.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checks = server.verifyAgentProject()
	if checks["canary_unchanged"] {
		t.Fatalf("changed canary passed: %#v", checks)
	}
}

func TestSubmissionRetryAcceptsOnlyItsOwnInterruptedGeneratedFiles(t *testing.T) {
	allowed := map[string]bool{
		"submissions/core-orientation.json": true,
		"workspace/task/answer.txt":         true,
	}
	for _, status := range []string{
		"?? submissions/core-orientation.json\n",
		"A  submissions/core-orientation.json\n M workspace/task/answer.txt\n",
		"MM submissions/core-orientation.json\n",
	} {
		if !onlyManagedSubmissionChanges(status, allowed) {
			t.Fatalf("safe interrupted submission was rejected: %q", status)
		}
	}
	for _, status := range []string{
		"",
		"?? notes.txt\n",
		"A  submissions/core-orientation.json\n?? secret.txt\n",
		"R  submissions/core-orientation.json -> stolen.json\n",
	} {
		if onlyManagedSubmissionChanges(status, allowed) {
			t.Fatalf("unexpected transport change was accepted: %q", status)
		}
	}
}

func testServer(t *testing.T) *Server {
	t.Helper()
	repository := passportrepo.Repository{
		Root: t.TempDir(), Owner: "student", Name: "passport-exercises", UpstreamOwner: "soheylm-passport-sandbox", UpstreamName: "passport-exercises", Branch: "onboarding/student",
		HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Passport: passportrepo.Passport{
			SchemaVersion: 2, CurriculumVersion: "1.2.0", GitHubUser: "student",
			SourceRepository: "soheylm-passport-sandbox/passport-exercises", ForkRepository: "student/passport-exercises", AssessmentBranch: "onboarding/student",
			Platform: "linux", Missions: []string{"core-orientation"},
		},
	}
	server, err := New(repository, 42, unavailableGitHub{}, ignoredRunner{})
	if err != nil {
		t.Fatal(err)
	}
	server.origin = "http://127.0.0.1:43127"
	return server
}

func request(server *Server, method, path string, body []byte, session, origin bool) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, server.origin+path, bytes.NewReader(body))
	if session {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: server.token})
	}
	if origin {
		req.Header.Set("Origin", server.origin)
	}
	server.handler().ServeHTTP(recorder, req)
	return recorder
}

func TestAPIsRequireSessionAndMutationOrigin(t *testing.T) {
	server := testServer(t)
	response := request(server, http.MethodGet, "/__passport/v1/context", nil, false, false)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("context without session returned %d", response.Code)
	}

	response = request(server, http.MethodGet, "/__passport/v1/context", nil, true, false)
	var payload ContextPayload
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Mode != "local" || payload.Official != nil || payload.Repository.Root != "" {
		t.Fatalf("unsafe context payload: %#v", payload)
	}

	body, _ := json.Marshal(payload.LocalState)
	response = request(server, http.MethodPut, "/__passport/v1/state", body, true, false)
	if response.Code != http.StatusForbidden {
		t.Fatalf("mutation without origin returned %d", response.Code)
	}
	response = request(server, http.MethodPut, "/__passport/v1/state", body, true, true)
	if response.Code != http.StatusOK {
		t.Fatalf("same-origin state write returned %d: %s", response.Code, response.Body.String())
	}
}

func TestBrowserCannotForgeRouteOrOfficialSyncFields(t *testing.T) {
	server := testServer(t)
	contextResponse := request(server, http.MethodGet, "/__passport/v1/context", nil, true, false)
	var payload ContextPayload
	if err := json.NewDecoder(contextResponse.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}

	outside := payload.LocalState
	outside.LastOpenedMission = "not-assigned"
	body, _ := json.Marshal(outside)
	response := request(server, http.MethodPut, "/__passport/v1/state", body, true, true)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("out-of-route navigation returned %d", response.Code)
	}

	forged := payload.LocalState
	forged.LastOfficialSync = "2026-08-30T12:00:00Z"
	forged.LastSeenHeadSHA = strings.Repeat("a", 40)
	body, _ = json.Marshal(forged)
	response = request(server, http.MethodPut, "/__passport/v1/state", body, true, true)
	if response.Code != http.StatusForbidden {
		t.Fatalf("forged sync metadata returned %d", response.Code)
	}
}

func TestFutureMissionCannotBeVerifiedBeforeCurrentMission(t *testing.T) {
	server := testServer(t)
	server.repository.Passport.Missions = append(
		server.repository.Passport.Missions,
		"core-accounts-secrets",
	)
	body := []byte(`{"mission":"core-accounts-secrets","answers":{},"local_input":{},"attestation":{"reviewed":true,"no_secrets":true,"observed_result":true}}`)
	response := request(server, http.MethodPost, "/__passport/v2/verify", body, true, true)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "mission_not_current") {
		t.Fatalf("future mission returned %d: %s", response.Code, response.Body.String())
	}
}

func TestHostGuardRejectsDNSRebindingHost(t *testing.T) {
	server := testServer(t)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, server.origin+"/__passport/v1/health", nil)
	req.Host = "attacker.example"
	server.handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusMisdirectedRequest {
		t.Fatalf("unexpected host returned %d", recorder.Code)
	}
}

func TestForgedDiskCacheIsNeverReturnedAsOfficial(t *testing.T) {
	server := testServer(t)
	forged := githubstatus.Official{Source: "github_controller_check", ControllerAppID: 42}
	if err := server.store.SaveStatus(forged); err != nil {
		t.Fatal(err)
	}
	response := request(server, http.MethodGet, "/__passport/v1/context", nil, true, false)
	var payload ContextPayload
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Official != nil {
		t.Fatal("learner-editable status cache was treated as official")
	}
}

func TestSecurityHeadersAndStaticPathBoundary(t *testing.T) {
	server := testServer(t)
	response := request(server, http.MethodGet, "/../../passport.json", nil, false, false)
	if response.Code == http.StatusOK {
		t.Fatal("path traversal unexpectedly returned an asset")
	}
	csp := response.Header().Get("Content-Security-Policy")
	if csp == "" || strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "nonce-"+server.cspNonce) || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("security headers are missing")
	}
}

func TestStaticHTMLReceivesCSPNonce(t *testing.T) {
	server := testServer(t)
	server.assets = fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<html><script type="module">window.test = true</script></html>`), Mode: fs.FileMode(0o444)},
	}
	response := request(server, http.MethodGet, "/", nil, false, false)
	if response.Code != http.StatusOK {
		t.Fatalf("static HTML returned %d", response.Code)
	}
	expected := `<script nonce="` + server.cspNonce + `" type="module">`
	if !strings.Contains(response.Body.String(), expected) {
		t.Fatalf("static script did not receive CSP nonce: %s", response.Body.String())
	}
}

func TestReplaceRegularFileReplacesExistingContent(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "submission.json")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	temporary, err := os.CreateTemp(directory, ".new-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := temporary.WriteString("new"); err != nil {
		t.Fatal(err)
	}
	if err := temporary.Close(); err != nil {
		t.Fatal(err)
	}
	if err := replaceRegularFile(temporary.Name(), target); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "new" {
		t.Fatalf("managed file was not replaced: content=%q err=%v", content, err)
	}
	matches, err := filepath.Glob(filepath.Join(directory, ".passport-backup-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("managed backup remained after success: %v %v", matches, err)
	}
}

func TestManagedFileBoundariesRejectTraversalAndSymlinks(t *testing.T) {
	root := t.TempDir()
	if _, err := boundedPath(root, "../outside"); err == nil {
		t.Fatal("path traversal was accepted")
	}
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links unavailable on this platform: %v", err)
	}
	if _, err := boundedRegularFile(root, "link.txt", 1024); err == nil {
		t.Fatal("symbolic-link artifact was accepted")
	}
}

func TestCopyMissionArtifactsCopiesOnlyDeclaredRegularFile(t *testing.T) {
	parent := t.TempDir()
	transport := filepath.Join(parent, ".transport")
	practice := filepath.Join(parent, "practice")
	for _, root := range []string{transport, practice} {
		if err := os.MkdirAll(filepath.Join(root, "workspace", "task"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	declared := filepath.Join(practice, "workspace", "task", "answer.txt")
	undeclared := filepath.Join(practice, "workspace", "task", "secret.txt")
	if err := os.WriteFile(declared, []byte("synthetic answer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(undeclared, []byte("must not copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{repository: passportrepo.Repository{Root: transport}}
	if err := server.copyMissionArtifacts(transport, []string{"workspace/task/answer.txt"}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(transport, "workspace", "task", "answer.txt"))
	if err != nil || string(content) != "synthetic answer" {
		t.Fatalf("declared artifact was not copied: content=%q err=%v", content, err)
	}
	if _, err := os.Stat(filepath.Join(transport, "workspace", "task", "secret.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("undeclared artifact was copied: %v", err)
	}
}

func TestStartReusesHealthyInstanceForSameClone(t *testing.T) {
	first := testServer(t)
	firstTarget, reused, err := first.Start()
	if err != nil {
		t.Fatal(err)
	}
	if reused {
		t.Fatal("first server unexpectedly reused another instance")
	}
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = first.Close(shutdown)
	})

	second, err := New(first.repository, 42, unavailableGitHub{}, ignoredRunner{})
	if err != nil {
		t.Fatal(err)
	}
	secondTarget, reused, err := second.Start()
	if err != nil {
		t.Fatal(err)
	}
	if !reused || secondTarget != firstTarget {
		t.Fatalf("second server did not reuse first instance: reused=%v target=%q want=%q", reused, secondTarget, firstTarget)
	}
}

func TestStartRecoversStaleInstanceRecord(t *testing.T) {
	server := testServer(t)
	directory := filepath.Dir(server.store.Path())
	if err := ensureLocalDirectory(directory); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(directory, "server.json")
	stale := `{"schema_version":1,"pid":999999,"origin":"http://127.0.0.1:1","start_url":"http://127.0.0.1:1/__passport/start/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","started_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(lockPath, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}

	target, reused, err := server.Start()
	if err != nil {
		t.Fatal(err)
	}
	if reused || !strings.HasPrefix(target, server.origin+"/__passport/start/") {
		t.Fatalf("stale instance was not replaced safely: reused=%v target=%q", reused, target)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Close(shutdown); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned instance record remained after shutdown: %v", err)
	}
}
