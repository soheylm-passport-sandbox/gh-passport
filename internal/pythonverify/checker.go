// Package pythonverify runs only the embedded trusted AST checker, never learner code.
package pythonverify

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const Contract = "python-change/2"
const SourcePath = "workspace/python_project/passport_example.py"
const TestPath = "workspace/python_project/tests/test_passport_example.py"

//go:embed checker.py
var checker []byte

const bridge = `
if __name__ == "__main__":
    import base64
    import json
    import sys
    try:
        raw = sys.stdin.buffer.read(524289)
        if len(raw) > 524288:
            raise ValueError()
        data = json.loads(raw)
        if not isinstance(data, dict) or set(data) != {"files", "changed_paths", "file_modes"}:
            raise ValueError()
        files = data["files"]
        if not isinstance(files, dict) or set(files) != PATHS:
            raise ValueError()
        decoded = {path: base64.b64decode(value, validate=True) for path, value in files.items()}
        result = assess_remote(decoded, data["changed_paths"], data["file_modes"])
        print(json.dumps(result))
    except Exception:
        print(json.dumps({"contract": CONTRACT, "status": "blocked", "code": "transport", "message": "The trusted checker could not read this bounded snapshot. Reopen and retry; no work was submitted."}))
`

type Snapshot struct {
	Files        map[string][]byte `json:"files"`
	ChangedPaths []string          `json:"changed_paths"`
	FileModes    map[string]string `json:"file_modes"`
}

type Result struct {
	Contract      string `json:"contract"`
	Status        string `json:"status"`
	Code          string `json:"code"`
	Message       string `json:"message,omitempty"`
	TestMethods   int    `json:"test_methods,omitempty"`
	Regression    []int  `json:"regression,omitempty"`
	BaselineFails bool   `json:"baseline_fails_regression,omitempty"`
}

func (r Result) Passed() bool {
	return r.Contract == Contract && r.Status == "pass" && r.Code == "accepted" && r.TestMethods >= 2 && len(r.Regression) == 3 && r.BaselineFails
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 16384 {
		return 0, errors.New("checker output exceeded its limit")
	}
	return b.Buffer.Write(p)
}

// Assess executes a shipped script in isolated Python mode from a private empty
// directory. Learner source is opaque stdin data, never imported or evaluated.
// The controller recomputes independently; local results are not authoritative.
func Assess(parent context.Context, interpreter string, snapshot Snapshot) (Result, error) {
	payload, err := json.Marshal(snapshot)
	if err != nil || len(payload) > 524288 {
		return Result{}, errors.New("invalid checker snapshot")
	}
	dir, err := os.MkdirTemp("", "passport-python-checker-")
	if err != nil {
		return Result{}, errors.New("cannot prepare trusted checker")
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0700); err != nil {
		return Result{}, err
	}
	path := filepath.Join(dir, "checker.py")
	script := append(append([]byte{}, checker...), []byte(bridge)...)
	if err := os.WriteFile(path, script, 0600); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, interpreter, "-I", "-S", path)
	command.Dir = dir
	// Keep platform process requirements, discard Python/import and token env.
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "SYSTEMROOT=" + os.Getenv("SYSTEMROOT"), "WINDIR=" + os.Getenv("WINDIR")}
	command.Stdin = bytes.NewReader(payload)
	output := &boundedOutput{}
	command.Stdout = output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return Result{}, errors.New("trusted Python checker stopped safely")
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	decoder.DisallowUnknownFields()
	var result Result
	if err := decoder.Decode(&result); err != nil {
		return Result{}, errors.New("invalid trusted checker response")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF || result.Contract != Contract {
		return Result{}, errors.New("unexpected checker identity or output")
	}
	switch result.Status {
	case "pass":
		if !result.Passed() {
			return Result{}, errors.New("incomplete checker evidence")
		}
	case "needs_work", "unsupported", "blocked":
		if result.Code == "" || result.Message == "" {
			return Result{}, errors.New("missing checker feedback")
		}
	default:
		return Result{}, errors.New("unknown checker status")
	}
	return result, nil
}
