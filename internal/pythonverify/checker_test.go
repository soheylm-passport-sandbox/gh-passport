package pythonverify

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSharedCasesThroughActualGoAdapter(t *testing.T) {
	interpreter, err := exec.LookPath("python3")
	if err != nil {
		interpreter, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("Python interpreter unavailable")
	}
	// Fixture bytes are data in the real adapter, including unsafe cases.
	path := filepath.Join("..", "..", "..", "..", "..", "agent_wiki", "assessment_candidates", "python_change", "cases.json")
	raw, err := os.ReadFile(path)
	if err != nil { // Exported extension carries the same fixture locally.
		raw, err = os.ReadFile("cases.json")
	}
	if err != nil {
		t.Fatal(err)
	}
	var packet struct {
		Cases []struct {
			ID     string `json:"id"`
			Source string `json:"source"`
			Tests  string `json:"tests"`
			Status string `json:"expected_status"`
			Code   string `json:"expected_code"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	for _, c := range packet.Cases {
		t.Run(c.ID, func(t *testing.T) {
			result, err := Assess(context.Background(), interpreter, Snapshot{
				Files:        map[string][]byte{SourcePath: []byte(c.Source), TestPath: []byte(c.Tests)},
				ChangedPaths: []string{SourcePath, TestPath}, FileModes: map[string]string{SourcePath: "100644", TestPath: "100644"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != c.Status || result.Code != c.Code {
				t.Fatalf("got %s/%s; expected %s/%s", result.Status, result.Code, c.Status, c.Code)
			}
		})
	}
}

func TestNoLearnerImportsFromWorkingDirectory(t *testing.T) {
	interpreter, err := exec.LookPath("python3")
	if err != nil {
		interpreter, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("Python unavailable")
	}
	// A hostile local import path must not affect the trusted checker.
	folder := t.TempDir()
	marker := filepath.Join(folder, "unexpected-import")
	source := "open(" + string(mustJSON(marker)) + ", 'w').write('unsafe synthetic import fixture')"
	if err := os.WriteFile(filepath.Join(folder, "sitecustomize.py"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PYTHONPATH", folder)
	result, err := Assess(context.Background(), interpreter, Snapshot{Files: map[string][]byte{}, ChangedPaths: []string{}, FileModes: map[string]string{}})
	if err != nil || result.Status != "blocked" {
		t.Fatalf("not blocked: %v %#v", err, result)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("untrusted import ran")
	}
}
func mustJSON(s string) []byte { b, _ := json.Marshal(s); return b }
