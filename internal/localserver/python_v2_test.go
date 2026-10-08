package localserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/passportrepo"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/pythonverify"
)

func TestPythonV2UsesRealGitAndIsolatedModel(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		python, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("Python unavailable")
	}
	root := filepath.Join(t.TempDir(), "practice folder with spaces")
	if err := os.MkdirAll(filepath.Join(root, "workspace/python_project/tests"), 0700); err != nil {
		t.Fatal(err)
	}
	write := func(relative, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(relative)), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source := "def total_memory_gib(cpus, memory_per_cpu_gib):\n    if cpus <= 0 or memory_per_cpu_gib <= 0:\n        raise ValueError('positive')\n    return memory_per_cpu_gib * cpus\n"
	tests := "import unittest\nfrom passport_example import total_memory_gib\nclass Memory(unittest.TestCase):\n    def test_one_cpu(self):\n        self.assertEqual(total_memory_gib(1, 3), 3)\n    def test_two_cpus(self):\n        self.assertEqual(total_memory_gib(2, 5), 10)\n"
	write(pythonverify.SourcePath, "# synthetic baseline\n")
	write(pythonverify.TestPath, "# synthetic baseline\n")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Synthetic", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Synthetic", "GIT_COMMITTER_EMAIL=test@example.invalid")
		if raw, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, raw)
		}
	}
	git("init")
	git("config", "core.autocrlf", "false")
	git("add", ".")
	git("-c", "core.hooksPath="+os.DevNull, "commit", "-m", "test: baseline")
	write(pythonverify.SourcePath, source)
	write(pythonverify.TestPath, tests)
	server := &Server{runner: passportrepo.ExecRunner{}}
	result, bounded, err := server.verifyPythonProjectAt(root, python)
	if err != nil || !bounded || !result.Passed() {
		t.Fatalf("valid work rejected: %v %v %#v", err, bounded, result)
	}
	write("workspace/python_project/unrelated.txt", "synthetic extra file")
	result, bounded, err = server.verifyPythonProjectAt(root, python)
	if err != nil || bounded || result.Passed() || result.Code != "scope" {
		t.Fatalf("extra untracked file accepted: %v %v %#v", err, bounded, result)
	}
}
