// Package slurmreview reviews fictional 9.0.0 headers, never executing them.
package slurmreview

import (
	"embed"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/gpureview"
)

//go:embed contract.json
var files embed.FS
var Contract struct {
	MaxBytes int `json:"max_bytes"`
	Profiles map[string]struct {
		Path     string
		Allowed  []string
		Messages map[string]string
	}
}
var commonOrder = []string{"bounded_file", "header_layout", "unique_directives", "supported_options", "single_allocation"}
var orders = map[string][]string{
	"array": append(append([]string{}, commonOrder...), "account", "array_exact", "output_unique", "error_unique", "safe_resources"),
	"gpu":   append(append([]string{}, commonOrder...), "account", "gpu_model", "no_partition", "cpu_limit", "memory_limit", "time_limit", "output_log", "error_log"),
}

func init() {
	raw, err := files.ReadFile("contract.json")
	if err != nil {
		panic(err)
	}
	if err = json.Unmarshal(raw, &Contract); err != nil {
		panic(err)
	}
}
func failed(profile string) map[string]bool {
	out := map[string]bool{}
	for _, key := range orders[profile] {
		out[key] = false
	}
	return out
}

const space = " \t\r\n\v\f"

var prefix = regexp.MustCompile(`^#SBATCH[ \t]+`)
var option = regexp.MustCompile(`^(--[a-z][a-z0-9-]*)(?:=|[ \t]+)([^ \t\r\v\f]+)$`)
var token = regexp.MustCompile(`^[A-Za-z0-9_./%:,+=-]+$`)

func CheckText(raw []byte, profile string) map[string]bool {
	checks := failed(profile)
	spec, known := Contract.Profiles[profile]
	if !known || len(raw) >= Contract.MaxBytes || !utf8.Valid(raw) {
		return checks
	}
	for _, key := range commonOrder {
		checks[key] = true
	}
	d := map[string]string{}
	body := false
	allowed := map[string]bool{}
	for _, key := range spec.Allowed {
		allowed[key] = true
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.Trim(line, space)
		if strings.HasPrefix(line, "#SBATCH") {
			if body {
				checks["header_layout"] = false
				continue
			}
			if !prefix.MatchString(line) {
				checks["header_layout"] = false
				continue
			}
			m := option.FindStringSubmatch(prefix.ReplaceAllString(line, ""))
			if m == nil || !token.MatchString(m[2]) {
				checks["header_layout"] = false
				checks["supported_options"] = false
				continue
			}
			key, value := m[1], m[2]
			if !allowed[key] {
				checks["supported_options"] = false
			}
			if _, exists := d[key]; exists {
				checks["unique_directives"] = false
				continue
			}
			d[key] = value
		} else if line != "" && !strings.HasPrefix(line, "#") {
			body = true
		}
	}
	for _, key := range []string{"--nodes", "--ntasks"} {
		if value, exists := d[key]; exists && value != "1" {
			checks["single_allocation"] = false
		}
	}
	if profile == "gpu" {
		lines := []string{}
		for key, value := range d {
			lines = append(lines, "#SBATCH "+key+"="+value)
		}
		for key, value := range gpureview.CheckText([]byte(strings.Join(lines, "\n"))) {
			if key != "bounded_file" {
				checks[key] = value
			}
		}
	} else {
		checks["account"] = d["--account"] == "es_fuge"
		checks["array_exact"] = d["--array"] == "0-9%1"
		checks["output_unique"] = strings.Contains(d["--output"], "%A") && strings.Contains(d["--output"], "%a")
		checks["error_unique"] = strings.Contains(d["--error"], "%A") && strings.Contains(d["--error"], "%a")
		checks["safe_resources"] = d["--time"] == "00:10:00" && d["--cpus-per-task"] == "1" && strings.EqualFold(d["--mem-per-cpu"], "1G")
	}
	return checks
}
func Feedback(checks map[string]bool, profile string) string {
	for _, key := range orders[profile] {
		if !checks[key] {
			return Contract.Profiles[profile].Messages[key]
		}
	}
	return ""
}
func CheckFile(root, profile string) map[string]bool {
	spec, known := Contract.Profiles[profile]
	if !known {
		return failed(profile)
	}
	for _, rel := range []string{"", "workspace", "workspace/slurm", spec.Path} {
		info, err := os.Lstat(filepath.Join(root, rel))
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return failed(profile)
		}
		if rel == spec.Path {
			if !info.Mode().IsRegular() {
				return failed(profile)
			}
		} else if !info.IsDir() {
			return failed(profile)
		}
	}
	confined, err := os.OpenRoot(root)
	if err != nil {
		return failed(profile)
	}
	defer confined.Close()
	file, err := confined.Open(spec.Path)
	if err != nil {
		return failed(profile)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, int64(Contract.MaxBytes)))
	if err != nil {
		return failed(profile)
	}
	return CheckText(raw, profile)
}
