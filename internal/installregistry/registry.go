package installregistry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type Record struct {
	SchemaVersion int    `json:"schema_version"`
	TransportRoot string `json:"transport_root"`
	UpdatedAt     string `json:"updated_at"`
}

func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("cannot locate the user home directory")
	}
	var directory string
	switch runtime.GOOS {
	case "windows":
		directory = os.Getenv("LOCALAPPDATA")
		if directory == "" {
			directory = filepath.Join(home, "AppData", "Local")
		}
		directory = filepath.Join(directory, "IDEALPassport")
	case "darwin":
		directory = filepath.Join(home, "Library", "Application Support", "IDEALPassport")
	default:
		directory = os.Getenv("XDG_STATE_HOME")
		if directory == "" {
			directory = filepath.Join(home, ".local", "state")
		}
		directory = filepath.Join(directory, "ideal-passport")
	}
	return filepath.Join(directory, "registry.json"), nil
}

func Save(transportRoot string) error {
	absolute, err := filepath.Abs(transportRoot)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(filepath.Join(absolute, "passport.json")); err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to register a folder without a regular passport.json")
	}
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	value := Record{1, absolute, time.Now().UTC().Format(time.RFC3339)}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".registry-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
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
	return os.Rename(name, path)
}

func Load() (Record, error) {
	path, err := Path()
	if err != nil {
		return Record{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Record{}, fmt.Errorf("read local passport registry: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 4096 {
		return Record{}, errors.New("local passport registry is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return Record{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var value Record
	if err := decoder.Decode(&value); err != nil {
		return Record{}, fmt.Errorf("parse local passport registry: %w", err)
	}
	if value.SchemaVersion != 1 || !filepath.IsAbs(value.TransportRoot) {
		return Record{}, errors.New("local passport registry has invalid fields")
	}
	return value, nil
}
