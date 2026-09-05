package launcherupdate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type staticRunner struct {
	output []byte
	err    error
}

func (runner staticRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return runner.output, runner.err
}

func TestMain(m *testing.M) {
	mode := os.Getenv("PASSPORT_UPDATE_TEST_MODE")
	if mode != "" {
		if len(os.Args) < 2 {
			os.Exit(2)
		}
		switch os.Args[1] {
		case "extension":
			if mode == "fail" {
				os.Exit(2)
			}
			source, _ := os.Executable()
			raw, err := os.ReadFile(source)
			if err != nil || os.WriteFile(os.Getenv("PASSPORT_UPDATE_TEST_TARGET"), raw, 0o700) != nil {
				os.Exit(2)
			}
			manifest := fmt.Sprintf(
				"owner: %s\nname: %s\nhost: %s\ntag: %s\npath: %s\n",
				TrustedOwner, TrustedRepository, TrustedHost,
				os.Getenv("PASSPORT_UPDATE_TEST_VERSION"), os.Getenv("PASSPORT_UPDATE_TEST_TARGET"),
			)
			if os.WriteFile(os.Getenv("PASSPORT_UPDATE_TEST_MANIFEST"), []byte(manifest), 0o600) != nil {
				os.Exit(2)
			}
			os.Exit(0)
		case "version":
			fmt.Printf("{\"version\":%q,\"curriculum_version\":%q}\n", os.Getenv("PASSPORT_UPDATE_TEST_VERSION"), "2.1.1")
			os.Exit(0)
		case "open":
			if mode == "open-fail" {
				os.Exit(2)
			}
			path := os.Getenv(ReadyFileEnv)
			token := os.Getenv(ReadyTokenEnv)
			if path == "" || token == "" || SignalReady(path, token) != nil {
				os.Exit(2)
			}
			os.Exit(0)
		default:
			os.Exit(2)
		}
	}
	os.Exit(m.Run())
}

func marker(t *testing.T, version string, curricula ...string) string {
	t.Helper()
	value, err := CompatibilityMarker(version, curricula)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func asset(tag, name, digest string) releaseAsset {
	return releaseAsset{
		Name:               name,
		BrowserDownloadURL: "https://github.com/" + TrustedOwner + "/" + TrustedRepository + "/releases/download/" + tag + "/" + name,
		Digest:             "sha256:" + digest,
		Size:               12_345,
	}
}

func TestSelectCompatibleChoosesHighestExactCurriculum(t *testing.T) {
	digest := strings.Repeat("a", 64)
	name := "gh-passport-windows-amd64.exe"
	releases := []release{
		{TagName: "v0.7.0", Body: marker(t, "v0.7.0", "3.0.0"), Assets: []releaseAsset{asset("v0.7.0", name, digest)}},
		{TagName: "v0.6.0", Body: marker(t, "v0.6.0", "2.1.1"), Assets: []releaseAsset{asset("v0.6.0", name, digest)}},
		{TagName: "v0.5.0", Body: marker(t, "v0.5.0", "2.1.1"), Assets: []releaseAsset{asset("v0.5.0", name, digest)}},
	}
	selected, err := selectCompatible(releases, "v0.4.2", "2.1.1", "windows", "amd64")
	if err != nil || selected == nil || selected.Version != "v0.6.0" {
		t.Fatalf("selected = %#v, err = %v", selected, err)
	}
}

func TestSelectCompatibleRejectsUntrustedAssetMetadata(t *testing.T) {
	digest := strings.Repeat("b", 64)
	name := "gh-passport-linux-amd64"
	badURL := asset("v0.4.3", name, digest)
	badURL.BrowserDownloadURL = "https://example.com/gh-passport-linux-amd64"
	badDigest := asset("v0.4.4", name, digest)
	badDigest.Digest = "sha256:not-a-digest"
	releases := []release{
		{TagName: "v0.4.3", Body: marker(t, "v0.4.3", "2.1.1"), Assets: []releaseAsset{badURL}},
		{TagName: "v0.4.4", Body: marker(t, "v0.4.4", "2.1.1"), Assets: []releaseAsset{badDigest}},
	}
	selected, err := selectCompatible(releases, "v0.4.2", "2.1.1", "linux", "amd64")
	if err != nil || selected != nil {
		t.Fatalf("selected = %#v, err = %v", selected, err)
	}
}

func TestCompatibilityMarkerRejectsUnknownOrDuplicateData(t *testing.T) {
	unknown := map[string]any{
		"schema_version":                 1,
		"release_version":                "v0.4.3",
		"compatible_curriculum_versions": []string{"2.1.1"},
		"browser_supplied_url":           "https://example.com",
	}
	raw, _ := json.Marshal(unknown)
	body := "<!-- ideal-passport-release:v1 " + base64.RawURLEncoding.EncodeToString(raw) + " -->"
	if _, err := parseCompatibility(body); err == nil {
		t.Fatal("unknown compatibility fields should fail")
	}
	valid := marker(t, "v0.4.3", "2.1.1")
	if _, err := parseCompatibility(valid + "\n" + valid); err == nil {
		t.Fatal("duplicate compatibility markers should fail")
	}
	if _, err := parseCompatibility(valid + "\n<!-- ideal-passport-release:v2 invalid -->"); err == nil {
		t.Fatal("a second malformed compatibility marker should fail")
	}
	if _, err := CompatibilityMarker("v0.4.3", nil); err == nil {
		t.Fatal("empty curriculum compatibility should fail")
	}
	if _, err := CompatibilityMarker("v0.4.3", []string{"2.1.1", "2.1.1"}); err == nil {
		t.Fatal("duplicate curriculum compatibility should fail")
	}
}

func TestSignalReadyRejectsUntrustedLocationOrToken(t *testing.T) {
	root := t.TempDir()
	if err := SignalReady(filepath.Join(root, "reopen-ready.json"), strings.Repeat("a", 32)); err == nil {
		t.Fatal("a readiness marker outside a generated update directory should fail")
	}
	directory := filepath.Join(root, "update-20260817T010203Z-0123456789abcdef")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SignalReady(filepath.Join(directory, "reopen-ready.json"), "not-a-token"); err == nil {
		t.Fatal("an invalid readiness token should fail")
	}
}

func TestPlatformAssetNamesAreExact(t *testing.T) {
	checks := map[string]string{
		"windows/amd64": "gh-passport-windows-amd64.exe",
		"darwin/arm64":  "gh-passport-darwin-arm64",
		"linux/amd64":   "gh-passport-linux-amd64",
	}
	for platform, expected := range checks {
		parts := strings.Split(platform, "/")
		actual, err := platformAssetName(parts[0], parts[1])
		if err != nil || actual != expected {
			t.Fatalf("%s = %q, %v", platform, actual, err)
		}
	}
	if _, err := platformAssetName("freebsd", "amd64"); err == nil {
		t.Fatal("unsupported platform should fail")
	}
}

func TestPrepareCreatesPrivatePlanFromTrustedRelease(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".passport-local"), 0o700); err != nil {
		t.Fatal(err)
	}
	extension := filepath.Join(root, "extension", "gh-passport")
	if runtime.GOOS == "windows" {
		extension += ".exe"
	}
	if err := os.MkdirAll(filepath.Dir(extension), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extension, []byte("trusted current launcher"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf("owner: %s\nname: %s\nhost: %s\ntag: v0.4.2\npath: %s\n", TrustedOwner, TrustedRepository, TrustedHost, extension)
	if err := os.WriteFile(filepath.Join(filepath.Dir(extension), "manifest.yml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	githubCLI := filepath.Join(root, "bin", "gh")
	if runtime.GOOS == "windows" {
		githubCLI += ".exe"
	}
	if err := os.MkdirAll(filepath.Dir(githubCLI), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(githubCLI, []byte("fake gh"), 0o700); err != nil {
		t.Fatal(err)
	}
	name, err := platformAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	digest := strings.Repeat("d", 64)
	releaseItem := release{
		TagName: "v0.4.3",
		Body:    marker(t, "v0.4.3", "2.1.1"),
		Assets:  []releaseAsset{asset("v0.4.3", name, digest)},
	}
	releaseJSON, err := json.Marshal([]release{releaseItem})
	if err != nil {
		t.Fatal(err)
	}
	service := Service{
		RepositoryRoot: root, CurrentVersion: "v0.4.2", CurriculumVersion: "2.1.1",
		Runner: staticRunner{output: releaseJSON}, OperatingSystem: runtime.GOOS, Architecture: runtime.GOARCH,
		executable: func() (string, error) { return extension, nil },
		lookPath:   func(string) (string, error) { return githubCLI, nil },
	}
	prepared, err := service.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := readPlan(prepared.PlanPath)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Candidate.Digest != digest || plan.Candidate.AssetName != name || prepared.Version != "v0.4.3" {
		t.Fatalf("private plan lost trusted release data: %#v", plan.Candidate)
	}
	for _, path := range []string{prepared.HelperPath, prepared.PlanPath, plan.RollbackBinary, plan.RollbackManifest} {
		if !within(filepath.Join(root, ".passport-local", "updates"), path) {
			t.Fatalf("prepared update escaped private state: %s", path)
		}
	}
}

func TestApplyVerifiesInstalledBinaryAndRecordsSuccess(t *testing.T) {
	planPath, plan := updateFixture(t)
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := fileSHA256(testBinary)
	if err != nil {
		t.Fatal(err)
	}
	plan.Candidate.Digest = digest
	if err := writePrivateJSON(planPath, plan); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PASSPORT_UPDATE_TEST_MODE", "success")
	t.Setenv("PASSPORT_UPDATE_TEST_TARGET", plan.ExecutablePath)
	t.Setenv("PASSPORT_UPDATE_TEST_MANIFEST", plan.ManifestPath)
	t.Setenv("PASSPORT_UPDATE_TEST_VERSION", plan.Candidate.Version)
	if err := Apply(planPath); err != nil {
		t.Fatal(err)
	}
	result := readResult(plan.StatusPath)
	if result == nil || result.Status != "updated" || result.Version != plan.Candidate.Version {
		t.Fatalf("unexpected update result: %#v", result)
	}
	installedDigest, err := fileSHA256(plan.ExecutablePath)
	if err != nil || installedDigest != digest {
		t.Fatalf("installed digest = %q, %v", installedDigest, err)
	}
}

func TestApplyRestoresPreviousBinaryWhenInstallFails(t *testing.T) {
	planPath, plan := updateFixture(t)
	if err := writePrivateJSON(planPath, plan); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PASSPORT_UPDATE_TEST_MODE", "fail")
	if err := Apply(planPath); err == nil {
		t.Fatal("failed installer should trigger rollback")
	}
	raw, err := os.ReadFile(plan.ExecutablePath)
	if err != nil || string(raw) != "previous launcher" {
		t.Fatalf("rollback binary = %q, %v", raw, err)
	}
	result := readResult(plan.StatusPath)
	if result == nil || result.Status != "rolled_back" || result.Version != plan.CurrentVersion {
		t.Fatalf("unexpected rollback result: %#v", result)
	}
}

func TestApplyRestoresPreviousBinaryWhenReopenFails(t *testing.T) {
	planPath, plan := updateFixture(t)
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := fileSHA256(testBinary)
	if err != nil {
		t.Fatal(err)
	}
	plan.Candidate.Digest = digest
	if err := writePrivateJSON(planPath, plan); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PASSPORT_UPDATE_TEST_MODE", "open-fail")
	t.Setenv("PASSPORT_UPDATE_TEST_TARGET", plan.ExecutablePath)
	t.Setenv("PASSPORT_UPDATE_TEST_MANIFEST", plan.ManifestPath)
	t.Setenv("PASSPORT_UPDATE_TEST_VERSION", plan.Candidate.Version)
	if err := Apply(planPath); err == nil {
		t.Fatal("a launcher that cannot reopen should trigger rollback")
	}
	raw, err := os.ReadFile(plan.ExecutablePath)
	if err != nil || string(raw) != "previous launcher" {
		t.Fatalf("rollback binary = %q, %v", raw, err)
	}
	result := readResult(plan.StatusPath)
	if result == nil || result.Status != "rolled_back" || !strings.Contains(result.Message, "could not reopen") {
		t.Fatalf("unexpected rollback result: %#v", result)
	}
}

func TestReadPlanRejectsPathAndRollbackTampering(t *testing.T) {
	planPath, plan := updateFixture(t)
	plan.ManifestPath = filepath.Join(filepath.Dir(plan.ManifestPath), "other.yml")
	if err := writePrivateJSON(planPath, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := readPlan(planPath); err == nil {
		t.Fatal("a detached manifest path should fail")
	}

	planPath, plan = updateFixture(t)
	if err := os.WriteFile(plan.RollbackBinary, []byte("tampered launcher"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateJSON(planPath, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := readPlan(planPath); err == nil {
		t.Fatal("a tampered rollback launcher should fail")
	}
}

func updateFixture(t *testing.T) (string, updatePlan) {
	t.Helper()
	root := t.TempDir()
	local := filepath.Join(root, ".passport-local")
	directory := filepath.Join(local, "updates", "update-20260817T010203Z-0123456789abcdef")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	executableName := "gh-passport"
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}
	executable := filepath.Join(root, "extension", executableName)
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("current launcher"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(filepath.Dir(executable), "manifest.yml")
	manifest := fmt.Sprintf("owner: %s\nname: %s\nhost: %s\ntag: v0.4.2\npath: %s\n", TrustedOwner, TrustedRepository, TrustedHost, executable)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	rollbackBinary := filepath.Join(directory, "rollback-binary")
	if err := os.WriteFile(rollbackBinary, []byte("previous launcher"), 0o700); err != nil {
		t.Fatal(err)
	}
	rollbackManifest := filepath.Join(directory, "rollback-manifest.yml")
	if err := os.WriteFile(rollbackManifest, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name, err := platformAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		// CI only targets the five supported release platforms.
		t.Skip(err)
	}
	githubCLI := filepath.Join(root, "bin", "gh")
	if runtime.GOOS == "windows" {
		githubCLI += ".exe"
	}
	if err := os.MkdirAll(filepath.Dir(githubCLI), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := copyRegular(testBinary, githubCLI, 0o700); err != nil {
		t.Fatal(err)
	}
	rollbackDigest, err := fileSHA256(rollbackBinary)
	if err != nil {
		t.Fatal(err)
	}
	plan := updatePlan{
		SchemaVersion:     1,
		RepositoryRoot:    root,
		ExecutablePath:    executable,
		ManifestPath:      manifestPath,
		RollbackBinary:    rollbackBinary,
		RollbackDigest:    rollbackDigest,
		RollbackManifest:  rollbackManifest,
		StatusPath:        filepath.Join(local, "update-status.json"),
		LogPath:           filepath.Join(directory, "update.log"),
		GitHubCLIPath:     githubCLI,
		CurrentVersion:    "v0.4.2",
		CurriculumVersion: "2.1.1",
		Candidate: Candidate{
			Version:     "v0.4.3",
			AssetName:   name,
			DownloadURL: "https://github.com/" + TrustedOwner + "/" + TrustedRepository + "/releases/download/v0.4.3/" + name,
			Digest:      strings.Repeat("c", 64),
			Size:        12345,
		},
	}
	return filepath.Join(directory, "plan.json"), plan
}
