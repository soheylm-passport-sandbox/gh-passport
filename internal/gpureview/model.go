// Package gpureview checks a bounded fictional request; it executes no script.
package gpureview

import (
	"embed"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

//go:embed contract.json
var contractFS embed.FS
var Contract struct {
	Path     string
	MaxBytes int `json:"max_bytes"`
	Messages map[string]string
}
var order = []string{"bounded_file", "account", "gpu_model", "no_partition", "cpu_limit", "memory_limit", "time_limit", "output_log", "error_log"}

func init() {
	raw, err := contractFS.ReadFile("contract.json")
	if err != nil {
		panic(err)
	}
	if err = json.Unmarshal(raw, &Contract); err != nil {
		panic(err)
	}
}

const space = " \t\r\n\v\f"

func number(raw string, maximum int) (int, bool) {
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(raw) {
		return 0, false
	}
	raw = strings.TrimLeft(raw, "0")
	if raw == "" {
		raw = "0"
	}
	if len(raw) > len(strconv.Itoa(maximum)) {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value >= 1 && value <= maximum
}
func failed() map[string]bool {
	checks := map[string]bool{}
	for _, key := range order {
		checks[key] = false
	}
	return checks
}
func CheckText(raw []byte) map[string]bool {
	checks := failed()
	if len(raw) >= Contract.MaxBytes || !utf8.Valid(raw) {
		return checks
	}
	checks["bounded_file"] = true
	d := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.Trim(line, space)
		if !strings.HasPrefix(line, "#SBATCH ") {
			continue
		}
		line = strings.Trim(strings.TrimPrefix(line, "#SBATCH "), space)
		if key, value, ok := strings.Cut(line, "="); ok {
			d[strings.Trim(key, space)] = strings.Trim(value, space)
		} else {
			parts := regexp.MustCompile(`[ \t\r\v\f]+`).Split(line, -1)
			d[parts[0]] = strings.Join(parts[1:], " ")
		}
	}
	cpu, cpuOK := number(d["--cpus-per-task"], 16)
	mem := regexp.MustCompile(`^([1-9][0-9]*)[Gg]$`).FindStringSubmatch(d["--mem-per-cpu"])
	gib, memOK := 0, false
	if mem != nil {
		gib, memOK = number(mem[1], 64)
	}
	_, hasMem := d["--mem"]
	_, hasPartition := d["--partition"]
	tm := regexp.MustCompile(`^(?:([0-9]+)-)?([0-9]{1,2}):([0-9]{2}):([0-9]{2})$`).FindStringSubmatch(d["--time"])
	timeOK := false
	if tm != nil && strings.Trim(tm[1], "0") == "" {
		h, _ := strconv.Atoi(tm[2])
		m, _ := strconv.Atoi(tm[3])
		s, _ := strconv.Atoi(tm[4])
		seconds := h*3600 + m*60 + s
		timeOK = m < 60 && s < 60 && seconds > 0 && seconds <= 14400
	}
	checks["account"] = d["--account"] == "es_fuge"
	checks["gpu_model"] = regexp.MustCompile(`^(?:rtx_4090|rtx_3090|pro_6000):1$`).MatchString(d["--gpus"])
	checks["no_partition"] = !hasPartition
	checks["cpu_limit"] = cpuOK
	checks["memory_limit"] = cpuOK && memOK && gib <= 64/cpu && !hasMem
	checks["time_limit"] = timeOK
	checks["output_log"] = strings.Contains(d["--output"], "%x") && strings.Contains(d["--output"], "%j")
	checks["error_log"] = strings.Contains(d["--error"], "%x") && strings.Contains(d["--error"], "%j")
	return checks
}
func Feedback(checks map[string]bool) string {
	for _, key := range order {
		if !checks[key] {
			return Contract.Messages[key]
		}
	}
	return ""
}
func CheckFile(root string) map[string]bool {
	for _, rel := range []string{"", "workspace", "workspace/slurm", Contract.Path} {
		info, err := os.Lstat(filepath.Join(root, rel))
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return failed()
		}
		if rel == Contract.Path {
			if !info.Mode().IsRegular() {
				return failed()
			}
		} else if !info.IsDir() {
			return failed()
		}
	}
	confined, err := os.OpenRoot(root)
	if err != nil {
		return failed()
	}
	defer confined.Close()
	file, err := confined.Open(Contract.Path)
	if err != nil {
		return failed()
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, int64(Contract.MaxBytes)))
	if err != nil {
		return failed()
	}
	return CheckText(raw)
}
