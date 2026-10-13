package slurmreview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSharedCases(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	raw, err := os.ReadFile(filepath.Join(root, "tests/fixtures/slurm_review_cases.json"))
	if os.IsNotExist(err) {
		t.Skip("source-only parity fixtures")
	}
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ID, Profile string
		Text        *string
		Passed      bool
		Padding     int `json:"padding_bytes"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			root := t.TempDir()
			p := filepath.Join(root, Contract.Profiles[c.Profile].Path)
			if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
				t.Fatal(err)
			}
			if c.Text != nil {
				raw := []byte(*c.Text)
				if c.Padding > 0 {
					raw = append(raw, []byte("#"+strings.Repeat("x", c.Padding-len(raw)-1))...)
				}
				if err := os.WriteFile(p, raw, 0600); err != nil {
					t.Fatal(err)
				}
			} else if strings.HasSuffix(c.ID, "invalid-utf8") {
				os.WriteFile(p, []byte{255}, 0600)
			}
			checks := CheckFile(root, c.Profile)
			feedback := Feedback(checks, c.Profile)
			record, _ := json.Marshal(map[string]any{"id": c.ID, "checks": checks, "feedback": feedback, "passed": feedback == ""})
			fmt.Println("SLURM_PARITY:" + string(record))
			if (feedback == "") != c.Passed {
				t.Fatalf("unexpected acceptance: %+v %s", checks, feedback)
			}
		})
	}
}
func TestUnsafePaths(t *testing.T) {
	for _, profile := range []string{"gpu", "array"} {
		root := t.TempDir()
		p := filepath.Join(root, Contract.Profiles[profile].Path)
		os.MkdirAll(p, 0700)
		if CheckFile(root, profile)["bounded_file"] {
			t.Fatal("accepted directory")
		}
		os.Remove(p)
		other := filepath.Join(root, "other")
		os.WriteFile(other, []byte("fictional"), 0600)
		if err := os.Symlink(other, p); err != nil {
			t.Log("symlink unavailable", err)
			continue
		}
		if CheckFile(root, profile)["bounded_file"] {
			t.Fatal("followed symlink")
		}
	}
}
