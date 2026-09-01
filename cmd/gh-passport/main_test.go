package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
