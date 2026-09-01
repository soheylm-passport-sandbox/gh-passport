package localstate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestAtomicStateRoundTripAndPermissions(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	state := State{
		SchemaVersion:     1,
		PassportID:        "passport-id",
		CurriculumVersion: "1.2.0",
		RouteDigest:       "digest",
		LastOpenedMission: "core-orientation",
		ExpandedHelp:      []string{"ssh"},
		LastOfficialSync:  time.Now().UTC().Format(time.RFC3339),
		LastSeenHeadSHA:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LastOpenedMission != state.LastOpenedMission || loaded.LastSeenHeadSHA != state.LastSeenHeadSHA {
		t.Fatalf("state round trip mismatch: %#v", loaded)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	// Windows reports ACL-backed files with synthetic POSIX permission bits.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("local state is accessible beyond the user: %o", info.Mode().Perm())
	}
}

func TestStateRefusesSymlinkedLocalDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ordinary Windows test users may not create symlinks")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".passport-local")); err != nil {
		t.Fatal(err)
	}
	state := State{
		SchemaVersion: 1, PassportID: "p", CurriculumVersion: "1.2.0",
		RouteDigest: "digest", LastOpenedMission: "core-orientation", ExpandedHelp: []string{},
	}
	if err := New(root).Save(state); err == nil {
		t.Fatal("expected symlinked local state directory to be rejected")
	}
	if _, err := os.Stat(filepath.Join(outside, "state.json")); !os.IsNotExist(err) {
		t.Fatal("state escaped the passport repository")
	}
}

func TestInvalidStateFailsClosedAndCanBeBackedUp(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte(`{"schema_version":1,"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("expected malformed state to fail")
	}
	backup, err := store.BackupCorrupt()
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("expected a corrupt-state backup path")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatal(err)
	}
}

func TestStateRejectsCredentialLikeUnmodeledFields(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := `{"schema_version":1,"passport_id":"p","curriculum_version":"1.2.0","route_digest":"d","last_opened_mission":"core-orientation","expanded_help":[],"token":"not-allowed"}`
	if err := os.WriteFile(store.Path(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("expected unknown secret-like field to be rejected")
	}
}
