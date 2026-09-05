package launcherupdate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	TrustedOwner      = "soheylm-passport-sandbox"
	TrustedRepository = "gh-passport"
	TrustedHost       = "github.com"
	ReadyFileEnv      = "IDEAL_PASSPORT_UPDATE_READY_FILE"
	ReadyTokenEnv     = "IDEAL_PASSPORT_UPDATE_READY_TOKEN"
	maxReleasePayload = 4 << 20
	maxManifestSize   = 16 << 10
	maxLauncherSize   = 128 << 20
	reopenTimeout     = 15 * time.Second
)

var (
	stableVersionPattern   = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	releaseMarkerPattern   = regexp.MustCompile(`<!-- ideal-passport-release:v1 ([A-Za-z0-9_-]+) -->`)
	updateDirectoryPattern = regexp.MustCompile(`^update-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{16}$`)
)

// Runner executes authenticated GitHub CLI commands without exposing a token.
type Runner interface {
	Run(ctx context.Context, directory string, args ...string) ([]byte, error)
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
	Size               int64  `json:"size"`
}

type release struct {
	TagName    string         `json:"tag_name"`
	Body       string         `json:"body"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

type compatibility struct {
	SchemaVersion                int      `json:"schema_version"`
	ReleaseVersion               string   `json:"release_version"`
	CompatibleCurriculumVersions []string `json:"compatible_curriculum_versions"`
}

// Candidate is a release selected entirely from the trusted GitHub repository.
type Candidate struct {
	Version     string `json:"version"`
	AssetName   string `json:"asset_name"`
	DownloadURL string `json:"download_url"`
	Digest      string `json:"sha256"`
	Size        int64  `json:"size_bytes"`
}

type Result struct {
	Status          string `json:"status"`
	Version         string `json:"version"`
	PreviousVersion string `json:"previous_version,omitempty"`
	RecordedAt      string `json:"recorded_at"`
	Message         string `json:"message"`
}

type Status struct {
	State             string `json:"state"`
	CurrentVersion    string `json:"current_version"`
	CurriculumVersion string `json:"curriculum_version"`
	Available         *struct {
		Version string `json:"version"`
		Size    int64  `json:"size_bytes"`
	} `json:"available,omitempty"`
	LastResult *Result `json:"last_result,omitempty"`
}

type Service struct {
	RepositoryRoot    string
	CurrentVersion    string
	CurriculumVersion string
	Runner            Runner
	OperatingSystem   string
	Architecture      string
	executable        func() (string, error)
	lookPath          func(string) (string, error)
	now               func() time.Time
	once              sync.Once
}

type Prepared struct {
	Version    string
	HelperPath string
	PlanPath   string
	LogPath    string
}

type updatePlan struct {
	SchemaVersion     int       `json:"schema_version"`
	RepositoryRoot    string    `json:"repository_root"`
	ExecutablePath    string    `json:"executable_path"`
	ManifestPath      string    `json:"manifest_path"`
	RollbackBinary    string    `json:"rollback_binary"`
	RollbackDigest    string    `json:"rollback_digest"`
	RollbackManifest  string    `json:"rollback_manifest"`
	StatusPath        string    `json:"status_path"`
	LogPath           string    `json:"log_path"`
	GitHubCLIPath     string    `json:"github_cli_path"`
	CurrentVersion    string    `json:"current_version"`
	CurriculumVersion string    `json:"curriculum_version"`
	Candidate         Candidate `json:"candidate"`
}

type versionOutput struct {
	Version           string `json:"version"`
	CurriculumVersion string `json:"curriculum_version"`
}

type readyMarker struct {
	Token string `json:"token"`
}

func (service *Service) defaults() {
	service.once.Do(func() {
		if service.OperatingSystem == "" {
			service.OperatingSystem = runtime.GOOS
		}
		if service.Architecture == "" {
			service.Architecture = runtime.GOARCH
		}
		if service.executable == nil {
			service.executable = os.Executable
		}
		if service.lookPath == nil {
			service.lookPath = exec.LookPath
		}
		if service.now == nil {
			service.now = time.Now
		}
	})
}

func (service *Service) Check(ctx context.Context) Status {
	service.defaults()
	status := Status{
		State:             "unavailable",
		CurrentVersion:    service.CurrentVersion,
		CurriculumVersion: service.CurriculumVersion,
		LastResult:        readResult(service.statusPath()),
	}
	candidate, err := service.discover(ctx)
	if err != nil {
		return status
	}
	if candidate == nil {
		status.State = "up_to_date"
		return status
	}
	status.State = "available"
	status.Available = &struct {
		Version string `json:"version"`
		Size    int64  `json:"size_bytes"`
	}{Version: candidate.Version, Size: candidate.Size}
	return status
}

func (service *Service) discover(ctx context.Context) (*Candidate, error) {
	if service.Runner == nil {
		return nil, errors.New("GitHub runner is unavailable")
	}
	if _, ok := parseVersion(service.CurrentVersion); !ok {
		return nil, errors.New("the current launcher is not a stable release")
	}
	output, err := service.Runner.Run(
		ctx,
		"",
		"api",
		"repos/"+TrustedOwner+"/"+TrustedRepository+"/releases?per_page=30",
		"--method",
		"GET",
	)
	if err != nil {
		return nil, err
	}
	if len(output) == 0 || len(output) > maxReleasePayload {
		return nil, errors.New("release response is empty or exceeds the size limit")
	}
	var releases []release
	if err := json.Unmarshal(output, &releases); err != nil {
		return nil, errors.New("release response is not valid JSON")
	}
	return selectCompatible(releases, service.CurrentVersion, service.CurriculumVersion, service.OperatingSystem, service.Architecture)
}

func selectCompatible(releases []release, current, curriculum, operatingSystem, architecture string) (*Candidate, error) {
	currentVersion, ok := parseVersion(current)
	if !ok {
		return nil, errors.New("current version is not stable semver")
	}
	assetName, err := platformAssetName(operatingSystem, architecture)
	if err != nil {
		return nil, err
	}
	var selected *Candidate
	var selectedVersion [3]uint64
	for _, item := range releases {
		if item.Draft || item.Prerelease {
			continue
		}
		parsed, ok := parseVersion(item.TagName)
		if !ok || compareVersion(parsed, currentVersion) <= 0 {
			continue
		}
		metadata, err := parseCompatibility(item.Body)
		if err != nil || metadata.ReleaseVersion != item.TagName || !contains(metadata.CompatibleCurriculumVersions, curriculum) {
			continue
		}
		var matches []releaseAsset
		for _, asset := range item.Assets {
			if asset.Name == assetName {
				matches = append(matches, asset)
			}
		}
		if len(matches) != 1 || !validAsset(item.TagName, matches[0]) {
			continue
		}
		if selected != nil && compareVersion(parsed, selectedVersion) <= 0 {
			continue
		}
		asset := matches[0]
		selected = &Candidate{
			Version:     item.TagName,
			AssetName:   asset.Name,
			DownloadURL: asset.BrowserDownloadURL,
			Digest:      strings.TrimPrefix(asset.Digest, "sha256:"),
			Size:        asset.Size,
		}
		selectedVersion = parsed
	}
	return selected, nil
}

func validAsset(tag string, asset releaseAsset) bool {
	expectedURL := "https://github.com/" + TrustedOwner + "/" + TrustedRepository + "/releases/download/" + tag + "/" + asset.Name
	if asset.BrowserDownloadURL != expectedURL || asset.Size <= 0 || asset.Size > 128<<20 {
		return false
	}
	digest := strings.TrimPrefix(asset.Digest, "sha256:")
	return strings.HasPrefix(asset.Digest, "sha256:") && len(digest) == 64 && isLowerHex(digest)
}

func platformAssetName(operatingSystem, architecture string) (string, error) {
	if architecture != "amd64" && architecture != "arm64" {
		return "", fmt.Errorf("unsupported architecture %q", architecture)
	}
	suffix := ""
	if operatingSystem == "windows" {
		suffix = ".exe"
	} else if operatingSystem != "darwin" && operatingSystem != "linux" {
		return "", fmt.Errorf("unsupported operating system %q", operatingSystem)
	}
	return "gh-passport-" + operatingSystem + "-" + architecture + suffix, nil
}

func parseCompatibility(body string) (compatibility, error) {
	if strings.Count(body, "<!-- ideal-passport-release:") != 1 {
		return compatibility{}, errors.New("release has no unique compatibility marker")
	}
	matches := releaseMarkerPattern.FindAllStringSubmatch(body, -1)
	if len(matches) != 1 {
		return compatibility{}, errors.New("release has no unique compatibility marker")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(matches[0][1])
	if err != nil || len(encoded) > 4096 {
		return compatibility{}, errors.New("release compatibility marker is invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var value compatibility
	if err := decoder.Decode(&value); err != nil {
		return compatibility{}, errors.New("release compatibility data is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return compatibility{}, errors.New("release compatibility data has trailing content")
	}
	if value.SchemaVersion != 1 {
		return compatibility{}, errors.New("unsupported release compatibility schema")
	}
	if _, ok := parseVersion(value.ReleaseVersion); !ok || len(value.CompatibleCurriculumVersions) == 0 || len(value.CompatibleCurriculumVersions) > 8 {
		return compatibility{}, errors.New("release compatibility data has invalid fields")
	}
	seen := map[string]bool{}
	for _, version := range value.CompatibleCurriculumVersions {
		if !validCurriculumVersion(version) || seen[version] {
			return compatibility{}, errors.New("release compatibility data has invalid curriculum versions")
		}
		seen[version] = true
	}
	return value, nil
}

func CompatibilityMarker(releaseVersion string, curricula []string) (string, error) {
	if _, ok := parseVersion(releaseVersion); !ok {
		return "", errors.New("release version must be stable semver")
	}
	if len(curricula) == 0 || len(curricula) > 8 {
		return "", errors.New("one to eight curriculum versions are required")
	}
	seen := map[string]bool{}
	for _, curriculum := range curricula {
		if !validCurriculumVersion(curriculum) || seen[curriculum] {
			return "", errors.New("curriculum versions must be unique numeric semver")
		}
		seen[curriculum] = true
	}
	value := compatibility{1, releaseVersion, curricula}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return "<!-- ideal-passport-release:v1 " + base64.RawURLEncoding.EncodeToString(encoded) + " -->", nil
}

func (service *Service) Prepare(ctx context.Context) (Prepared, error) {
	service.defaults()
	candidate, err := service.discover(ctx)
	if err != nil {
		return Prepared{}, errors.New("a compatible release could not be verified")
	}
	if candidate == nil {
		return Prepared{}, errors.New("no compatible update is available")
	}
	executablePath, err := service.executable()
	if err != nil {
		return Prepared{}, errors.New("the installed launcher could not be located")
	}
	executablePath, err = filepath.Abs(executablePath)
	if err != nil {
		return Prepared{}, errors.New("the installed launcher path is invalid")
	}
	manifestPath := filepath.Join(filepath.Dir(executablePath), "manifest.yml")
	manifest, err := readManifest(manifestPath)
	if err != nil || manifest["owner"] != TrustedOwner || manifest["name"] != TrustedRepository || manifest["host"] != TrustedHost || !samePath(manifest["path"], executablePath) || manifest["tag"] != service.CurrentVersion {
		return Prepared{}, errors.New("the launcher is not the trusted GitHub CLI extension")
	}
	githubCLI, err := service.lookPath("gh")
	if err != nil {
		return Prepared{}, errors.New("GitHub CLI is not available")
	}
	githubCLI, err = resolveRegularExecutable(githubCLI)
	if err != nil {
		return Prepared{}, errors.New("the GitHub CLI path is invalid")
	}
	root, err := filepath.Abs(service.RepositoryRoot)
	if err != nil {
		return Prepared{}, errors.New("the passport folder is invalid")
	}
	updatesRoot := filepath.Join(root, ".passport-local", "updates")
	if err := ensurePrivateDirectory(filepath.Join(root, ".passport-local")); err != nil {
		return Prepared{}, err
	}
	if err := ensurePrivateDirectory(updatesRoot); err != nil {
		return Prepared{}, err
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return Prepared{}, errors.New("could not create a private update workspace")
	}
	directory := filepath.Join(updatesRoot, "update-"+service.now().UTC().Format("20060102T150405Z")+"-"+hex.EncodeToString(random))
	if err := os.Mkdir(directory, 0o700); err != nil {
		return Prepared{}, errors.New("could not create a private update workspace")
	}
	helperName := "gh-passport-update-helper"
	if service.OperatingSystem == "windows" {
		helperName += ".exe"
	}
	helperPath := filepath.Join(directory, helperName)
	rollbackBinary := filepath.Join(directory, "rollback-binary")
	if service.OperatingSystem == "windows" {
		rollbackBinary += ".exe"
	}
	rollbackManifest := filepath.Join(directory, "rollback-manifest.yml")
	if err := copyRegular(executablePath, helperPath, 0o700); err != nil {
		return Prepared{}, errors.New("could not prepare the detached update helper")
	}
	if err := copyRegular(executablePath, rollbackBinary, 0o700); err != nil {
		return Prepared{}, errors.New("could not prepare a verified rollback copy")
	}
	rollbackDigest, err := fileSHA256(rollbackBinary)
	if err != nil {
		return Prepared{}, errors.New("could not verify the rollback copy")
	}
	if err := copyRegular(manifestPath, rollbackManifest, 0o600); err != nil {
		return Prepared{}, errors.New("could not prepare a verified rollback manifest")
	}
	planPath := filepath.Join(directory, "plan.json")
	logPath := filepath.Join(directory, "update.log")
	plan := updatePlan{
		SchemaVersion: 1, RepositoryRoot: root, ExecutablePath: executablePath, ManifestPath: manifestPath,
		RollbackBinary: rollbackBinary, RollbackDigest: rollbackDigest, RollbackManifest: rollbackManifest, StatusPath: service.statusPath(),
		LogPath: logPath, GitHubCLIPath: githubCLI, CurrentVersion: service.CurrentVersion,
		CurriculumVersion: service.CurriculumVersion, Candidate: *candidate,
	}
	if err := writePrivateJSON(planPath, plan); err != nil {
		return Prepared{}, errors.New("could not write the private update plan")
	}
	return Prepared{Version: candidate.Version, HelperPath: helperPath, PlanPath: planPath, LogPath: logPath}, nil
}

func Launch(prepared Prepared) error {
	if prepared.HelperPath == "" || prepared.PlanPath == "" || prepared.LogPath == "" {
		return errors.New("update helper is incomplete")
	}
	log, err := os.OpenFile(prepared.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return errors.New("could not create the private update log")
	}
	command := exec.Command(prepared.HelperPath, "__apply-update", "--plan", prepared.PlanPath)
	command.Stdin = nil
	command.Stdout = log
	command.Stderr = log
	if err := command.Start(); err != nil {
		log.Close()
		return errors.New("could not start the detached update helper")
	}
	_ = command.Process.Release()
	return log.Close()
}

func Apply(planPath string) error {
	plan, err := readPlan(planPath)
	if err != nil {
		return err
	}
	time.Sleep(750 * time.Millisecond)
	install := exec.Command(
		plan.GitHubCLIPath,
		"extension", "install", TrustedOwner+"/"+TrustedRepository,
		"--force", "--pin", plan.Candidate.Version,
	)
	install.Dir = plan.RepositoryRoot
	install.Env = append(os.Environ(), "GH_PAGER=cat", "NO_COLOR=1")
	output, installErr := install.CombinedOutput()
	if installErr == nil {
		installErr = verifyInstalled(plan)
	}
	if installErr != nil {
		rollbackErr := restoreAndReopen(plan, "The update did not pass its safety checks. The previous launcher was restored.")
		if rollbackErr != nil {
			return fmt.Errorf("update failed and rollback could not be completed: %w", rollbackErr)
		}
		if len(output) > 0 {
			fmt.Fprintln(os.Stderr, strings.TrimSpace(string(output)))
		}
		return errors.New("the update failed its safety checks; the previous launcher was restored")
	}
	if err := writeResult(plan.StatusPath, Result{
		Status: "updated", Version: plan.Candidate.Version, PreviousVersion: plan.CurrentVersion,
		RecordedAt: time.Now().UTC().Format(time.RFC3339), Message: "The Passport launcher was updated. Your saved place and answers were kept.",
	}); err != nil {
		if rollbackErr := restoreAndReopen(plan, "The update could not record a safe result. The previous launcher was restored."); rollbackErr != nil {
			return fmt.Errorf("update result could not be recorded and rollback failed: %w", rollbackErr)
		}
		return errors.New("the update could not record a safe result; the previous launcher was restored")
	}
	if err := launchPassport(plan, true); err != nil {
		if rollbackErr := restoreAndReopen(plan, "The updated launcher could not reopen the Passport. The previous launcher was restored."); rollbackErr != nil {
			return fmt.Errorf("updated launcher could not reopen the Passport and rollback failed: %w", rollbackErr)
		}
		return errors.New("the updated launcher could not reopen the Passport; the previous launcher was restored")
	}
	return nil
}

func verifyInstalled(plan updatePlan) error {
	digest, err := fileSHA256(plan.ExecutablePath)
	if err != nil || digest != plan.Candidate.Digest {
		return errors.New("installed launcher checksum does not match the trusted release")
	}
	command := exec.Command(plan.ExecutablePath, "version", "--json")
	command.Dir = plan.RepositoryRoot
	output, err := command.Output()
	if err != nil || len(output) > 16<<10 {
		return errors.New("installed launcher did not report its version")
	}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	decoder.DisallowUnknownFields()
	var reported versionOutput
	if err := decoder.Decode(&reported); err != nil || reported.Version != plan.Candidate.Version || reported.CurriculumVersion != plan.CurriculumVersion {
		return errors.New("installed launcher is not compatible with this Passport")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("installed launcher returned unexpected version data")
	}
	manifest, err := readManifest(plan.ManifestPath)
	if err != nil || manifest["owner"] != TrustedOwner || manifest["name"] != TrustedRepository || manifest["host"] != TrustedHost || manifest["tag"] != plan.Candidate.Version || !samePath(manifest["path"], plan.ExecutablePath) {
		return errors.New("installed extension manifest is not trusted")
	}
	return nil
}

func rollback(plan updatePlan) error {
	digest, err := fileSHA256(plan.RollbackBinary)
	if err != nil || digest != plan.RollbackDigest {
		return errors.New("rollback launcher checksum does not match the prepared copy")
	}
	if err := copyRegular(plan.RollbackBinary, plan.ExecutablePath, 0o700); err != nil {
		return err
	}
	return copyRegular(plan.RollbackManifest, plan.ManifestPath, 0o600)
}

func restoreAndReopen(plan updatePlan, message string) error {
	if err := rollback(plan); err != nil {
		return err
	}
	if err := writeResult(plan.StatusPath, Result{
		Status: "rolled_back", Version: plan.CurrentVersion, RecordedAt: time.Now().UTC().Format(time.RFC3339),
		Message: message,
	}); err != nil {
		return err
	}
	_ = launchPassport(plan, false)
	return nil
}

func launchPassport(plan updatePlan, requireReady bool) error {
	if !requireReady {
		command := exec.Command(plan.ExecutablePath, "open")
		command.Dir = plan.RepositoryRoot
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Start(); err != nil {
			return errors.New("the restored launcher could not reopen the Passport")
		}
		return command.Process.Release()
	}
	readyPath := filepath.Join(filepath.Dir(plan.LogPath), "reopen-ready.json")
	if err := removeRegularIfPresent(readyPath); err != nil {
		return errors.New("the launcher readiness signal is unsafe")
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return errors.New("the launcher readiness token could not be created")
	}
	token := hex.EncodeToString(tokenBytes)
	command := exec.Command(plan.ExecutablePath, "open")
	command.Dir = plan.RepositoryRoot
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = append(os.Environ(), ReadyFileEnv+"="+readyPath, ReadyTokenEnv+"="+token)
	if err := command.Start(); err != nil {
		return errors.New("the launcher was installed but could not reopen the Passport")
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(reopenTimeout)
	defer timer.Stop()
	for {
		select {
		case waitErr := <-done:
			if readySignalMatches(readyPath, token) {
				return nil
			}
			if waitErr != nil {
				return errors.New("the updated launcher exited before its local server was ready")
			}
			return errors.New("the updated launcher stopped before its local server was ready")
		case <-ticker.C:
			if readySignalMatches(readyPath, token) {
				return nil
			}
		case <-timer.C:
			_ = command.Process.Kill()
			<-done
			return errors.New("the updated launcher did not become ready in time")
		}
	}
}

// SignalReady records that an updater-launched Passport server is listening.
func SignalReady(path, token string) error {
	if !filepath.IsAbs(path) || filepath.Base(path) != "reopen-ready.json" || !updateDirectoryPattern.MatchString(filepath.Base(filepath.Dir(path))) || len(token) != 32 || !isLowerHex(token) {
		return errors.New("update readiness signal is invalid")
	}
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	return writePrivateJSON(path, readyMarker{Token: token})
}

func readySignalMatches(path, token string) bool {
	var marker readyMarker
	return readBoundedJSON(path, 1024, &marker) == nil && marker.Token == token
}

func removeRegularIfPresent(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to remove a non-regular readiness signal")
	}
	return os.Remove(path)
}

func readPlan(path string) (updatePlan, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return updatePlan{}, errors.New("update plan path is invalid")
	}
	var plan updatePlan
	if err := readBoundedJSON(absolute, 64<<10, &plan); err != nil {
		return updatePlan{}, errors.New("update plan is invalid")
	}
	if plan.SchemaVersion != 1 || !allAbsolute(
		plan.RepositoryRoot, plan.ExecutablePath, plan.ManifestPath, plan.RollbackBinary,
		plan.RollbackManifest, plan.StatusPath, plan.LogPath, plan.GitHubCLIPath,
	) {
		return updatePlan{}, errors.New("update plan fields are invalid")
	}
	if !samePath(plan.ManifestPath, filepath.Join(filepath.Dir(plan.ExecutablePath), "manifest.yml")) || !validLauncherName(filepath.Base(plan.ExecutablePath)) || !validGitHubCLIName(filepath.Base(plan.GitHubCLIPath)) {
		return updatePlan{}, errors.New("update plan executable paths are invalid")
	}
	if len(plan.RollbackDigest) != 64 || !isLowerHex(plan.RollbackDigest) {
		return updatePlan{}, errors.New("update plan rollback checksum is invalid")
	}
	currentVersion, ok := parseVersion(plan.CurrentVersion)
	if !ok {
		return updatePlan{}, errors.New("update plan has an invalid current version")
	}
	candidateVersion, ok := parseVersion(plan.Candidate.Version)
	if !ok || compareVersion(candidateVersion, currentVersion) <= 0 || !validCurriculumVersion(plan.CurriculumVersion) {
		return updatePlan{}, errors.New("update plan has invalid version fields")
	}
	asset := releaseAsset{
		Name: plan.Candidate.AssetName, BrowserDownloadURL: plan.Candidate.DownloadURL,
		Digest: "sha256:" + plan.Candidate.Digest, Size: plan.Candidate.Size,
	}
	if !validAsset(plan.Candidate.Version, asset) {
		return updatePlan{}, errors.New("update plan has invalid release metadata")
	}
	directory := filepath.Dir(absolute)
	for _, path := range []string{plan.RollbackBinary, plan.RollbackManifest, plan.LogPath} {
		if !within(directory, path) {
			return updatePlan{}, errors.New("update plan escaped its private workspace")
		}
	}
	if !within(filepath.Join(plan.RepositoryRoot, ".passport-local"), absolute) || plan.StatusPath != filepath.Join(plan.RepositoryRoot, ".passport-local", "update-status.json") {
		return updatePlan{}, errors.New("update plan is outside the local Passport state")
	}
	manifest, err := readManifest(plan.RollbackManifest)
	if err != nil || manifest["owner"] != TrustedOwner || manifest["name"] != TrustedRepository || manifest["host"] != TrustedHost || manifest["tag"] != plan.CurrentVersion || !samePath(manifest["path"], plan.ExecutablePath) {
		return updatePlan{}, errors.New("rollback manifest is not trusted")
	}
	rollbackDigest, err := fileSHA256(plan.RollbackBinary)
	if err != nil || rollbackDigest != plan.RollbackDigest {
		return updatePlan{}, errors.New("rollback launcher is not the prepared copy")
	}
	if _, err := resolveRegularExecutable(plan.GitHubCLIPath); err != nil {
		return updatePlan{}, errors.New("GitHub CLI executable is not trusted")
	}
	return plan, nil
}

func readManifest(path string) (map[string]string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxManifestSize {
		return nil, errors.New("extension manifest is not a bounded regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	value := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || value[strings.TrimSpace(parts[0])] != "" {
			return nil, errors.New("extension manifest has invalid fields")
		}
		value[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return value, nil
}

func (service *Service) statusPath() string {
	return filepath.Join(service.RepositoryRoot, ".passport-local", "update-status.json")
}

func readResult(path string) *Result {
	var result Result
	if err := readBoundedJSON(path, 16<<10, &result); err != nil || (result.Status != "updated" && result.Status != "rolled_back") {
		return nil
	}
	return &result
}

func writeResult(path string, result Result) error {
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	return writePrivateJSON(path, result)
}

func writePrivateJSON(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	directory := filepath.Dir(path)
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("refusing to replace a non-regular private state file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".passport-update-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
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
	if runtime.GOOS == "windows" {
		_ = os.Remove(path)
	}
	return os.Rename(temporaryPath, path)
}

func readBoundedJSON(path string, limit int64, value any) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > limit {
		return errors.New("file is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, limit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("file contains trailing JSON")
	}
	return nil
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return errors.New("could not create a private update directory")
		}
		info, err = os.Lstat(path)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("the private update directory is not safe")
	}
	return os.Chmod(path, 0o700)
}

func copyRegular(source, destination string, mode os.FileMode) error {
	if samePath(source, destination) {
		return errors.New("update source and destination must differ")
	}
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxLauncherSize {
		return errors.New("update source is not a bounded regular file")
	}
	if destinationInfo, destinationErr := os.Lstat(destination); destinationErr == nil {
		if !destinationInfo.Mode().IsRegular() || destinationInfo.Mode()&os.ModeSymlink != 0 {
			return errors.New("update destination is not a regular file")
		}
	} else if !errors.Is(destinationErr, os.ErrNotExist) {
		return destinationErr
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	copied, copyErr := io.Copy(output, io.LimitReader(input, maxLauncherSize+1))
	if copyErr == nil && copied != info.Size() {
		copyErr = errors.New("update source changed while it was copied")
	}
	if syncErr := output.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := output.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return copyErr
	}
	return os.Chmod(destination, mode)
}

func fileSHA256(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxLauncherSize {
		return "", errors.New("launcher is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	read, err := io.Copy(hash, io.LimitReader(file, maxLauncherSize+1))
	if err != nil {
		return "", err
	}
	if read != info.Size() {
		return "", errors.New("launcher changed while it was verified")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func resolveRegularExecutable(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxLauncherSize {
		return "", errors.New("executable is not a bounded regular file")
	}
	return resolved, nil
}

func allAbsolute(paths ...string) bool {
	for _, path := range paths {
		if path == "" || !filepath.IsAbs(path) {
			return false
		}
	}
	return true
}

func validLauncherName(name string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(name, "gh-passport.exe")
	}
	return name == "gh-passport"
}

func validGitHubCLIName(name string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(name, "gh.exe")
	}
	return name == "gh"
}

func parseVersion(value string) ([3]uint64, bool) {
	matches := stableVersionPattern.FindStringSubmatch(value)
	if matches == nil {
		return [3]uint64{}, false
	}
	var result [3]uint64
	for index := 0; index < 3; index++ {
		parsed, err := strconv.ParseUint(matches[index+1], 10, 64)
		if err != nil {
			return [3]uint64{}, false
		}
		result[index] = parsed
	}
	return result, true
}

func compareVersion(left, right [3]uint64) int {
	for index := range left {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	return 0
}

func validCurriculumVersion(value string) bool {
	value = "v" + value
	_, ok := parseVersion(value)
	return ok
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func isLowerHex(value string) bool {
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func samePath(left, right string) bool {
	leftAbsolute, leftErr := filepath.Abs(left)
	rightAbsolute, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(leftAbsolute), filepath.Clean(rightAbsolute))
	}
	return filepath.Clean(leftAbsolute) == filepath.Clean(rightAbsolute)
}

func within(root, candidate string) bool {
	rootAbsolute, rootErr := filepath.Abs(root)
	candidateAbsolute, candidateErr := filepath.Abs(candidate)
	if rootErr != nil || candidateErr != nil {
		return false
	}
	relative, err := filepath.Rel(rootAbsolute, candidateAbsolute)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
