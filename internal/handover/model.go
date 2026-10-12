// Package handover verifies a fixed fictional inventory; it executes no document command.
package handover

import (
	"embed"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

//go:embed contract.json
var contractFS embed.FS

type Profile struct {
	SchemaVersion      int `json:"schema_version"`
	ID, Revision, Tree string
	Files, Objects     map[string]string
	GitConfig          string `json:"git_config"`
	MaxDocumentBytes   int    `json:"max_document_bytes"`
	ExpectedResult     string `json:"expected_result"`
}

var Contract = loadProfile()

func loadProfile() Profile {
	raw, err := contractFS.ReadFile("contract.json")
	if err != nil {
		panic(err)
	}
	var p Profile
	if err = json.Unmarshal(raw, &p); err != nil {
		panic(err)
	}
	return p
}

var Fields = []string{"Current owner", "Authorized successor", "Repository", "Revision", "Main approved data location", "Temporary locations to remove", "Environment definition", "Setup command", "Verification command", "Expected result", "Access owner", "Retention owner", "Temporary-copy deletion date", "Unresolved risk or limitation"}

const PosixSetup = "python3 -m venv --without-pip .venv"
const WindowsSetup = "python -m venv --without-pip .venv"
const PosixVerify = "./.venv/bin/python -I verify.py"
const WindowsVerify = `& .\.venv\Scripts\python.exe -I verify.py`

func Assess(raw []byte, revision string, today time.Time) []string {
	if len(raw) >= Contract.MaxDocumentBytes {
		return []string{"Keep the fictional handover document smaller than 40000 bytes."}
	}
	if !utf8.Valid(raw) {
		return []string{"Save the fictional handover as UTF-8."}
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	values := map[string]string{}
	findings := []string{}
	for _, field := range Fields {
		matches := []string{}
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, field+":") && !strings.ContainsRune(line, '\r') {
				matches = append(matches, strings.Trim(line[len(field)+1:], " \t"))
			}
		}
		if len(matches) != 1 || matches[0] == "" {
			findings = append(findings, "Complete "+field+" once, on its own line.")
		} else {
			values[field] = matches[0]
		}
	}
	if strings.Contains(text, "REPLACE_ME") || strings.Contains(text, "REPLACE_WITH_") {
		findings = append(findings, "Replace every fictional exercise placeholder.")
	}
	expected := []struct {
		field   string
		allowed []string
	}{{"Repository", []string{"local fictional fixture"}}, {"Revision", []string{revision}}, {"Main approved data location", []string{"inputs/measurements.csv"}}, {"Temporary locations to remove", []string{"scratch/"}}, {"Environment definition", []string{"environment.json"}}, {"Setup command", []string{PosixSetup, WindowsSetup}}, {"Verification command", []string{PosixVerify, WindowsVerify}}, {"Expected result", []string{Contract.ExpectedResult}}}
	for _, e := range expected {
		if value, ok := values[e.field]; ok {
			valid := false
			for _, a := range e.allowed {
				valid = valid || value == a
			}
			if !valid {
				findings = append(findings, "Check "+e.field+" against the provided project inventory; do not invent a location or result.")
			}
		}
	}
	if (values["Setup command"] == WindowsSetup && values["Verification command"] == PosixVerify) || (values["Setup command"] == PosixSetup && values["Verification command"] == WindowsVerify) {
		findings = append(findings, "Keep the setup and verification commands for the same operating system.")
	}
	value := values["Temporary-copy deletion date"]
	deletion, err := time.Parse("2006-01-02", value)
	day := today.UTC().Format("2006-01-02")
	if err != nil || deletion.Format("2006-01-02") != value || value <= day {
		findings = append(findings, "Choose a valid future planned deletion date for the fictional temporary copy; this approves no real deletion.")
	}
	return findings
}

// ReadDocument refuses links, non-regular files and escapes before bounded reading.
func ReadDocument(root string) ([]byte, error) {
	for _, rel := range []string{"", "workspace", "workspace/handover", "workspace/handover/project-handover.md"} {
		info, err := os.Lstat(filepath.Join(root, rel))
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("handover document path is missing or unsafe")
		}
		if rel == "workspace/handover/project-handover.md" {
			if !info.Mode().IsRegular() {
				return nil, errors.New("handover document is not a regular file")
			}
		} else if !info.IsDir() {
			return nil, errors.New("handover parent is not a directory")
		}
	}
	confined, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer confined.Close()
	file, err := confined.Open("workspace/handover/project-handover.md")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, int64(Contract.MaxDocumentBytes)))
}

// Template is the empty synthetic worksheet, not a completed learner answer.
func Template() string {
	text := "# Synthetic Project Handover\n\nUse only the supplied fictional inventory; no real paths, people or credentials.\n\n"
	lines := []string{}
	for _, field := range Fields {
		value := "REPLACE_ME"
		switch field {
		case "Repository":
			value = "local fictional fixture"
		case "Revision":
			value = "REPLACE_WITH_40_HEX_CHARACTERS"
		case "Temporary-copy deletion date":
			value = "REPLACE_WITH_YYYY-MM-DD"
		}
		lines = append(lines, field+": "+value)
	}
	return text + strings.Join(lines, "\n\n") + "\n"
}

// PrepareWorksheet creates only a missing regular worksheet and never replaces work.
func PrepareWorksheet(root string) error {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe worksheet root")
	}
	confined, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer confined.Close()
	for _, rel := range []string{"workspace", "workspace/handover"} {
		if err = confined.Mkdir(rel, 0700); err != nil && !os.IsExist(err) {
			return err
		}
		info, err = os.Lstat(filepath.Join(root, rel))
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe worksheet parent")
		}
	}
	rel := "workspace/handover/project-handover.md"
	file, err := confined.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		info, e := os.Lstat(filepath.Join(root, rel))
		if e != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe existing worksheet")
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(Template())
	return err
}
