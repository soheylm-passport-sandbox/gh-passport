package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/launcherupdate"
)

func TestVersionJSONIsMachineReadable(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = write
	callErr := printVersion([]string{"--json"})
	write.Close()
	os.Stdout = previous
	if callErr != nil {
		t.Fatal(callErr)
	}
	raw, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Version           string `json:"version"`
		CurriculumVersion string `json:"curriculum_version"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("version output is not JSON: %v: %s", err, raw)
	}
	if value.Version != version || value.CurriculumVersion != curriculumVersion {
		t.Fatalf("unexpected version output: %#v", value)
	}
}

func TestUpdateReadinessRequiresBothPrivateValues(t *testing.T) {
	t.Setenv(launcherupdate.ReadyFileEnv, "")
	t.Setenv(launcherupdate.ReadyTokenEnv, "")
	if err := signalUpdateReady(); err != nil {
		t.Fatalf("ordinary launch should not require update readiness: %v", err)
	}
	t.Setenv(launcherupdate.ReadyFileEnv, filepath.Join(t.TempDir(), "reopen-ready.json"))
	if err := signalUpdateReady(); err == nil {
		t.Fatal("an incomplete readiness request should fail")
	}
}

func TestDiagnosticBundleIsPrivateAndRedacted(t *testing.T) {
	root := filepath.Join(t.TempDir(), "student name with spaces")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := "ghp_not-a-real-secret"
	path, err := writeDiagnosticBundle(root, []doctorCheck{
		{Name: "passport-repository", Status: "blocked", Detail: root + " " + secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(filepath.Join(root, ".passport-local", "diagnostics"), path)
	if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
		t.Fatalf("bundle escaped private diagnostic directory: %q", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), root) || strings.Contains(string(raw), secret) || strings.Contains(string(raw), "Detail") {
		t.Fatalf("diagnostic bundle contains an unsanitized detail: %s", raw)
	}
	if !strings.Contains(string(raw), `"telemetry": "none"`) {
		t.Fatalf("diagnostic bundle does not state the telemetry policy: %s", raw)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("bundle permissions = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestDiagnosticBundleRejectsSymlinkedLocalDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not generally available to unprivileged Windows users")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".passport-local")); err != nil {
		t.Fatal(err)
	}
	if _, err := writeDiagnosticBundle(root, nil); err == nil {
		t.Fatal("expected symlinked local directory to be rejected")
	}
}

func TestParseDoctorOptions(t *testing.T) {
	options, err := parseDoctorOptions([]string{"--bundle", "--json"})
	if err != nil || !options.Bundle || !options.JSON {
		t.Fatalf("parseDoctorOptions returned %#v, %v", options, err)
	}
	if _, err := parseDoctorOptions([]string{"--bundle", "--bundle"}); err == nil {
		t.Fatal("duplicate --bundle should fail")
	}
}

func TestSubcommandHelpDoesNotExecuteCommand(t *testing.T) {
	for _, command := range []string{"start", "open", "status", "sync", "doctor"} {
		if err := run([]string{command, "--help"}); err != nil {
			t.Fatalf("%s --help returned %v", command, err)
		}
	}
}
