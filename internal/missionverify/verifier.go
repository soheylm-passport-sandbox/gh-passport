package missionverify

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxCatalogBytes = 4 << 20

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type Question struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Prompt   string   `json:"prompt"`
	Options  []Option `json:"options"`
	Answer   []string `json:"answer"`
	Critical bool     `json:"critical"`
	Feedback string   `json:"feedback"`
}

type Activity struct {
	Questions []Question `json:"questions"`
}

type Verification struct {
	LocalVerifier            string   `json:"local_verifier"`
	PassPercent              int      `json:"pass_percent"`
	CriticalMustPass         bool     `json:"critical_must_pass"`
	RequiresLiveConfirmation bool     `json:"requires_live_confirmation"`
	ReceiptChecks            []string `json:"receipt_checks"`
}

type SubmissionContract struct {
	Path          string   `json:"path"`
	ArtifactPaths []string `json:"artifact_paths"`
}

type Mission struct {
	Title        string             `json:"title"`
	Activity     Activity           `json:"activity"`
	Verification Verification       `json:"verification"`
	Submission   SubmissionContract `json:"submission"`
}

type Catalog struct {
	SchemaVersion     int                       `json:"schema_version"`
	CurriculumVersion string                    `json:"curriculum_version"`
	Missions          map[string]Mission        `json:"missions"`
	UniversalTracks   []string                  `json:"universal_tracks"`
	TrackDependencies map[string][]string       `json:"track_dependencies"`
	Responsibilities  map[string]Responsibility `json:"responsibilities"`
	Tracks            []Track                   `json:"tracks"`
}

type Responsibility struct {
	Title  string   `json:"title"`
	Tracks []string `json:"tracks"`
}

type Track struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Missions []string `json:"missions"`
}

type Attestation struct {
	Reviewed       bool `json:"reviewed"`
	NoSecrets      bool `json:"no_secrets"`
	ObservedResult bool `json:"observed_result"`
}

type Attempt struct {
	Mission     string              `json:"mission"`
	Answers     map[string][]string `json:"answers"`
	LocalInput  map[string]string   `json:"local_input"`
	Attestation Attestation         `json:"attestation"`
}

type Feedback struct {
	Question string `json:"question"`
	Correct  bool   `json:"correct"`
	Message  string `json:"message"`
}

type Result struct {
	Status         string         `json:"status"`
	ScorePercent   int            `json:"score_percent"`
	CriticalPassed bool           `json:"critical_passed"`
	Feedback       []Feedback     `json:"feedback"`
	Receipt        map[string]any `json:"receipt"`
	NextVariant    int            `json:"next_variant"`
}

func LoadCatalog(root string) (Catalog, error) {
	path := filepath.Join(root, "passport-curriculum.json")
	info, err := os.Lstat(path)
	if err != nil {
		return Catalog{}, fmt.Errorf("read mission catalog: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxCatalogBytes {
		return Catalog{}, errors.New("mission catalog is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return Catalog{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var value Catalog
	if err := decoder.Decode(&value); err != nil {
		return Catalog{}, fmt.Errorf("parse mission catalog: %w", err)
	}
	if value.SchemaVersion != 2 || value.CurriculumVersion == "" || len(value.Missions) == 0 {
		return Catalog{}, errors.New("mission catalog has an unsupported identity")
	}
	return value, nil
}

func Grade(catalog Catalog, attempt Attempt) (Mission, Result, error) {
	mission, ok := catalog.Missions[attempt.Mission]
	if !ok {
		return Mission{}, Result{}, errors.New("mission is not in the trusted catalog")
	}
	known := make(map[string]bool, len(mission.Activity.Questions))
	feedback := make([]Feedback, 0, len(mission.Activity.Questions))
	correct := 0
	criticalPassed := true
	for _, question := range mission.Activity.Questions {
		if !identifierPattern.MatchString(question.ID) || len(question.Options) < 2 {
			return Mission{}, Result{}, errors.New("mission catalog contains an invalid question")
		}
		known[question.ID] = true
		actual := attempt.Answers[question.ID]
		matched := equalAnswer(actual, question.Answer, question.Type == "order")
		if matched {
			correct++
			feedback = append(feedback, Feedback{question.ID, true, "Correct."})
		} else {
			if question.Critical {
				criticalPassed = false
			}
			feedback = append(feedback, Feedback{question.ID, false, question.Feedback})
		}
	}
	for question := range attempt.Answers {
		if !known[question] {
			return Mission{}, Result{}, errors.New("attempt contains an unknown answer")
		}
	}
	score := 0
	if len(mission.Activity.Questions) > 0 {
		score = (100*correct + len(mission.Activity.Questions)/2) / len(mission.Activity.Questions)
	}
	passed := score >= mission.Verification.PassPercent && (!mission.Verification.CriticalMustPass || criticalPassed)
	result := Result{
		Status:         "needs_work",
		ScorePercent:   score,
		CriticalPassed: criticalPassed,
		Feedback:       feedback,
		Receipt: map[string]any{
			"verifier": mission.Verification.LocalVerifier,
			"passed":   passed,
		},
	}
	if passed {
		result.Status = "ready_to_submit"
	}
	return mission, result, nil
}

func Resolve(catalog Catalog, responsibilities []string) ([]string, error) {
	selected := map[string]bool{}
	visiting := map[string]bool{}
	var addTrack func(string) error
	addTrack = func(id string) error {
		if selected[id] {
			return nil
		}
		if visiting[id] {
			return errors.New("track dependency cycle")
		}
		dependencies, ok := catalog.TrackDependencies[id]
		if !ok {
			return fmt.Errorf("unknown track %q", id)
		}
		visiting[id] = true
		for _, dependency := range dependencies {
			if err := addTrack(dependency); err != nil {
				return err
			}
		}
		delete(visiting, id)
		selected[id] = true
		return nil
	}
	for _, id := range catalog.UniversalTracks {
		if err := addTrack(id); err != nil {
			return nil, err
		}
	}
	seenResponsibilities := map[string]bool{}
	for _, key := range responsibilities {
		if seenResponsibilities[key] {
			return nil, errors.New("responsibility appears more than once")
		}
		seenResponsibilities[key] = true
		definition, ok := catalog.Responsibilities[key]
		if !ok {
			return nil, fmt.Errorf("unknown responsibility %q", key)
		}
		for _, id := range definition.Tracks {
			if err := addTrack(id); err != nil {
				return nil, err
			}
		}
	}
	missions := []string{}
	for _, track := range catalog.Tracks {
		if selected[track.ID] {
			missions = append(missions, track.Missions...)
		}
	}
	if len(missions) == 0 {
		return nil, errors.New("route resolved no missions")
	}
	return missions, nil
}

func ConfirmLive(mission Mission, input map[string]string) (map[string]any, error) {
	verifier := mission.Verification.LocalVerifier
	if !mission.Verification.RequiresLiveConfirmation {
		return map[string]any{"verifier": verifier, "passed": true, "checks": map[string]bool{"structured_activity": true}}, nil
	}
	checks := map[string]bool{}
	switch verifier {
	case "ssh_config":
		checks["key_only_marker"] = input["marker"] == "config-ok"
	case "euler_job":
		checks["job_id"] = regexp.MustCompile(`^[1-9][0-9]{2,}$`).MatchString(input["job_id"])
		checks["correct_account"] = input["account"] == "es_fuge"
		checks["owner_checked"] = input["owner_checked"] == "yes"
		checks["queue_inspected"] = input["queue_inspected"] == "yes"
		checks["completed"] = strings.EqualFold(input["state"], "COMPLETED")
		checks["zero_exit"] = input["exit_code"] == "0:0"
		checks["one_cpu"] = input["alloc_cpus"] == "1"
		checks["one_gib_per_cpu"] = regexp.MustCompile(`(?i)^1G(?:n)?$`).MatchString(input["req_mem"])
		checks["seff_inspected"] = input["seff_seen"] == "yes"
		checks["output_verified"] = input["output_marker"] == "5 squared is 25"
	case "euler_accounting":
		checks["job_id"] = regexp.MustCompile(`^[1-9][0-9]{2,}$`).MatchString(input["job_id"])
		checks["sacct_seen"] = input["sacct_seen"] == "yes"
		checks["seff_seen"] = input["seff_seen"] == "yes"
	case "nas_access":
		return confirmNAS(verifier, input)
	case "blade_receipt":
		checks["correct_host"] = strings.EqualFold(input["host"], "mavt-ide-s100w")
		checks["durable_copy"] = input["durable_copy"] == "yes"
		checks["temporary_removed"] = input["temporary_removed"] == "yes"
		checks["avoided_c_drive"] = input["avoided_c_drive"] == "yes"
	default:
		return nil, errors.New("this live verifier is not implemented by the launcher")
	}
	for _, passed := range checks {
		if !passed {
			return map[string]any{"verifier": verifier, "passed": false, "checks": checks}, nil
		}
	}
	return map[string]any{"verifier": verifier, "passed": true, "checks": checks}, nil
}

func confirmNAS(verifier string, input map[string]string) (map[string]any, error) {
	root := filepath.Clean(input["path"])
	username := strings.TrimSpace(input["username"])
	if root == "." || username == "" || strings.ContainsAny(username, `/\\`) || !strings.EqualFold(filepath.Base(root), username) {
		return map[string]any{"verifier": verifier, "passed": false, "checks": map[string]bool{"username_boundary": false}}, nil
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return map[string]any{"verifier": verifier, "passed": false, "checks": map[string]bool{"username_boundary": true, "real_directory": false}}, nil
	}
	nonce := make([]byte, 24)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	name := ".ideal-passport-probe-" + hex.EncodeToString(nonce[:6])
	probe := filepath.Join(root, name)
	file, err := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return map[string]any{"verifier": verifier, "passed": false, "checks": map[string]bool{"username_boundary": true, "write": false}}, nil
	}
	writeOK := false
	if _, err = file.Write(nonce); err == nil {
		writeOK = file.Sync() == nil
	}
	closeErr := file.Close()
	content, readErr := os.ReadFile(probe)
	readOK := readErr == nil && string(content) == string(nonce)
	removeErr := os.Remove(probe)
	_, residualErr := os.Lstat(probe)
	removed := removeErr == nil && errors.Is(residualErr, os.ErrNotExist)
	checks := map[string]bool{
		"username_boundary": true,
		"write":             writeOK && closeErr == nil,
		"read":              readOK,
		"removed":           removed,
	}
	digest := sha256.Sum256(nonce)
	passed := checks["write"] && checks["read"] && checks["removed"]
	return map[string]any{
		"verifier":         verifier,
		"passed":           passed,
		"checks":           checks,
		"challenge_digest": hex.EncodeToString(digest[:]),
	}, nil
}

func equalAnswer(actual, expected []string, ordered bool) bool {
	if len(actual) != len(expected) {
		return false
	}
	if ordered {
		for index := range actual {
			if actual[index] != expected[index] {
				return false
			}
		}
		return true
	}
	seen := make(map[string]int, len(actual))
	for _, item := range actual {
		seen[item]++
	}
	for _, item := range expected {
		seen[item]--
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}
