package localstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

const maxStateBytes = 64 << 10

var (
	missionPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	shaPattern     = regexp.MustCompile(`^[a-f0-9]{40}$`)
)

type State struct {
	SchemaVersion     int      `json:"schema_version"`
	PassportID        string   `json:"passport_id"`
	CurriculumVersion string   `json:"curriculum_version"`
	RouteDigest       string   `json:"route_digest"`
	LastOpenedMission string   `json:"last_opened_mission"`
	ExpandedHelp      []string `json:"expanded_help"`
	LastOfficialSync  string   `json:"last_official_sync,omitempty"`
	LastSeenHeadSHA   string   `json:"last_seen_head_sha,omitempty"`
}

type Store struct {
	root string
}

func New(root string) Store {
	return Store{root: root}
}

func (store Store) Path() string {
	return filepath.Join(store.root, ".passport-local", "state.json")
}

func (store Store) StatusCachePath() string {
	return filepath.Join(store.root, ".passport-local", "status-cache.json")
}

func (store Store) Load() (State, error) {
	path := store.Path()
	info, statErr := os.Lstat(path)
	if errors.Is(statErr, os.ErrNotExist) {
		return State{}, nil
	}
	if statErr != nil {
		return State{}, statErr
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxStateBytes {
		return State{}, errors.New("local state is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return State{}, err
	}
	defer file.Close()
	limited := io.LimitReader(file, maxStateBytes+1)
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("parse local state: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return State{}, errors.New("local state contains trailing JSON")
	}
	if err := state.Validate(); err != nil {
		return State{}, err
	}
	return state, nil
}

func (store Store) Save(state State) error {
	if err := state.Validate(); err != nil {
		return err
	}
	return store.writeJSON(store.Path(), state)
}

func (store Store) SaveStatus(value any) error {
	return store.writeJSON(store.StatusCachePath(), value)
}

func (store Store) BackupCorrupt() (string, error) {
	path := store.Path()
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return "", err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	backup := filepath.Join(
		filepath.Dir(path),
		"state.corrupt."+time.Now().UTC().Format("20060102T150405Z")+".json",
	)
	return backup, os.Rename(path, backup)
}

func (state State) Validate() error {
	if state.SchemaVersion == 0 && state.PassportID == "" && state.CurriculumVersion == "" {
		return nil
	}
	if state.SchemaVersion != 1 || state.PassportID == "" || state.CurriculumVersion == "" || state.RouteDigest == "" {
		return errors.New("local state has missing identity or unsupported schema")
	}
	if state.LastOpenedMission != "" && !missionPattern.MatchString(state.LastOpenedMission) {
		return errors.New("local state has an invalid mission")
	}
	if len(state.ExpandedHelp) > 100 {
		return errors.New("local state contains too many expanded help entries")
	}
	if state.LastSeenHeadSHA != "" && !shaPattern.MatchString(state.LastSeenHeadSHA) {
		return errors.New("local state has an invalid head SHA")
	}
	if state.LastOfficialSync != "" {
		if _, err := time.Parse(time.RFC3339, state.LastOfficialSync); err != nil {
			return errors.New("local state has an invalid sync timestamp")
		}
	}
	return nil
}

func (store Store) writeJSON(path string, value any) error {
	directory := filepath.Dir(path)
	if err := ensurePrivateDirectory(directory); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxStateBytes {
		return errors.New("local state exceeds 64 KiB")
	}
	temporary, err := os.CreateTemp(directory, ".passport-state-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}

func ensurePrivateDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err = os.Lstat(directory)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New(".passport-local must be a real directory inside the passport repository")
	}
	return nil
}
