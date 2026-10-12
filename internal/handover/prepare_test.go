package handover

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreparedHandoverIdentityAndRecovery(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project with spaces ' $ literal")
	if err := Prepare(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProject(root); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-c", "core.fsmonitor=false", "status", "--porcelain")
	cmd.Dir = root
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Git repository invalid: %v %s", err, raw)
	} else if strings.TrimSpace(string(raw)) != "?? .handover-profile" {
		t.Fatalf("unexpected tracked work: %s", raw)
	}
	before, _ := os.ReadFile(filepath.Join(root, "verify.py"))
	if err := Prepare(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "verify.py")
	if err := os.WriteFile(file, []byte("# altered synthetic file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(context.Background(), root); err == nil {
		t.Fatal("altered project accepted")
	}
	after, _ := os.ReadFile(file)
	if string(after) != "# altered synthetic file" {
		t.Fatal("existing work overwritten")
	}
	os.WriteFile(file, before, 0600)
	if err := Prepare(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	// This clone comes from the real prepared repository. No network or student's repository.
	fresh := filepath.Join(parent, "fresh successor")
	cmd = exec.Command("git", "clone", "--no-local", root, fresh)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v %s", err, raw)
	}
	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = fresh
	raw, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(raw)) != Contract.Revision {
		t.Fatal("fresh clone did not preserve its actual revision")
	}
	if _, err = os.Stat(filepath.Join(fresh, ".handover-profile")); !os.IsNotExist(err) {
		t.Fatal("untracked author marker copied")
	}
	out, _ := json.Marshal(map[string]any{"revision": Contract.Revision, "fresh_clone": true, "safe_reuse": true, "changed_work_preserved": true})
	fmt.Println("HANDOVER_PREPARED:" + string(out))
}

func TestPreparationRejectsUnknownIncompleteAndLinkedTargets(t *testing.T) {
	parent := t.TempDir()
	for _, name := range []string{"unknown", "incomplete"} {
		root := filepath.Join(parent, name)
		os.Mkdir(root, 0700)
		os.WriteFile(filepath.Join(root, "personal.txt"), []byte("synthetic personal work"), 0600)
		if err := Prepare(context.Background(), root); err == nil {
			t.Fatal("existing unknown target accepted")
		}
		raw, _ := os.ReadFile(filepath.Join(root, "personal.txt"))
		if string(raw) != "synthetic personal work" {
			t.Fatal("unknown data altered")
		}
	}
	root := filepath.Join(parent, "link")
	if err := os.Symlink(parent, root); err == nil {
		if err := Prepare(context.Background(), root); err == nil {
			t.Fatal("linked target accepted")
		}
	}
	ready := filepath.Join(parent, "ready")
	if err := Prepare(context.Background(), ready); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(ready, ".git", "config"), []byte("[alias]\n unsafe = !echo unsafe\n"), 0600)
	if err := Prepare(context.Background(), ready); err == nil {
		t.Fatal("tampered Git configuration accepted")
	}
}

func TestModelBoundAndEmptyOwner(t *testing.T) {
	if len(Assess(make([]byte, 40000), Contract.Revision, time.Now())) != 1 {
		t.Fatal("bound")
	}
	if len(Assess([]byte{255}, Contract.Revision, time.Now())) != 1 {
		t.Fatal("UTF-8")
	}
}

func TestPreparedFreshCloneActuallyReproduces(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("local reproduction requires Python 3.9+")
	}
	parent := t.TempDir()
	source := filepath.Join(parent, "source project")
	fresh := filepath.Join(parent, "successor ' $ folder")
	if err = Prepare(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "clone", "--no-local", "--", source, fresh)
	if raw, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("clone: %v %s", e, raw)
	}
	// Make author files unavailable before environment creation and execution.
	if err = os.Rename(source, source+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(python, "-m", "venv", "--without-pip", ".venv")
	cmd.Dir = fresh
	if raw, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("venv: %v %s", e, raw)
	}
	interpreter := filepath.Join(fresh, ".venv", "bin", "python")
	if os.PathSeparator == '\\' {
		interpreter = filepath.Join(fresh, ".venv", "Scripts", "python.exe")
	}
	run := func() (string, error) {
		cmd := exec.Command(interpreter, "-I", "verify.py")
		cmd.Dir = fresh
		raw, e := cmd.CombinedOutput()
		return strings.TrimSpace(string(raw)), e
	}
	output, err := run()
	if err != nil || output != Contract.ExpectedResult {
		t.Fatalf("run=%s error=%v", output, err)
	}
	input := filepath.Join(fresh, "inputs", "measurements.csv")
	raw, _ := os.ReadFile(input)
	if err = os.Rename(input, input+"-missing"); err != nil {
		t.Fatal(err)
	}
	if _, err = run(); err == nil {
		t.Fatal("missing input accepted")
	}
	os.WriteFile(input, []byte(strings.ReplaceAll(string(raw), "synthetic-c,6", "synthetic-c,7")), 0600)
	if output, err = run(); err != nil || output != "measurements=3 total=13" {
		t.Fatalf("wrong input proof: %s %v", output, err)
	}
	os.WriteFile(input, raw, 0600)
	if output, err = run(); err != nil || output != Contract.ExpectedResult {
		t.Fatal("retry failed")
	}
	fmt.Println("HANDOVER_REPRODUCTION: actual_prepared_clone source_unavailable fresh_venv missing_input wrong_output_exit_zero retry")
}
func TestInterruptedPreparationPreservesIncompleteFolder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "interrupted")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Prepare(ctx, root); err == nil {
		t.Fatal("cancelled preparation succeeded")
	}
	marker := filepath.Join(root, ".handover-incomplete")
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err = Prepare(context.Background(), root); err == nil {
		t.Fatal("incomplete folder overwritten")
	}
	after, _ := os.ReadFile(marker)
	if string(before) != string(after) {
		t.Fatal("incomplete marker changed")
	}
}
