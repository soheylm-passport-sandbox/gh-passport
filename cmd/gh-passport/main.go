package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/githubstatus"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/localserver"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/localstate"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/passportrepo"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/starter"
)

var (
	version           = "0.1.0-dev"
	controllerAppID   = "0"
	curriculumVersion = "1.2.0"
)

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type doctorOptions struct {
	JSON   bool
	Bundle bool
}

type diagnosticReport struct {
	SchemaVersion     int               `json:"schema_version"`
	GeneratedAt       string            `json:"generated_at"`
	LauncherVersion   string            `json:"launcher_version"`
	CurriculumVersion string            `json:"curriculum_version"`
	OperatingSystem   string            `json:"operating_system"`
	Architecture      string            `json:"architecture"`
	Checks            []diagnosticCheck `json:"checks"`
	Telemetry         string            `json:"telemetry"`
}

type diagnosticCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gh passport:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	command := "open"
	if len(arguments) > 0 && !strings.HasPrefix(arguments[0], "-") {
		command = arguments[0]
		arguments = arguments[1:]
	}
	switch command {
	case "start":
		return start(arguments)
	case "open":
		return open(arguments)
	case "status":
		return status(arguments)
	case "sync":
		return syncStatus(arguments)
	case "doctor":
		return doctor(arguments)
	case "version":
		fmt.Printf("gh-passport %s (curriculum %s, controller-app-id %s, bridge-contract v1)\n", version, curriculumVersion, controllerAppID)
		return nil
	case "help", "--help", "-h":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", command)
	}
}

func start(arguments []string) error {
	options := starter.Options{}
	noBrowser := false
	for index := 0; index < len(arguments); index++ {
		switch arguments[index] {
		case "--yes":
			options.AssumeYes = true
		case "--no-browser":
			noBrowser = true
		case "--directory", "--platform", "--responsibility":
			if index+1 >= len(arguments) {
				return fmt.Errorf("%s requires a value", arguments[index])
			}
			value := arguments[index+1]
			index++
			switch arguments[index-1] {
			case "--directory":
				options.Directory = value
			case "--platform":
				options.Platform = value
			case "--responsibility":
				options.Responsibilities = append(options.Responsibilities, value)
			}
		default:
			return fmt.Errorf("unknown start option %q", arguments[index])
		}
	}
	result, err := starter.Run(options)
	if err != nil {
		return err
	}
	if err := os.Chdir(result.Directory); err != nil {
		return fmt.Errorf("enter passport folder: %w", err)
	}
	if noBrowser {
		fmt.Println("Run `gh passport open` from the folder above when you are ready.")
		return nil
	}
	return open(nil)
}

func open(arguments []string) error {
	noBrowser := false
	for _, argument := range arguments {
		if argument == "--no-browser" {
			noBrowser = true
			continue
		}
		return fmt.Errorf("unknown open option %q", argument)
	}
	repository, appID, err := contextForCurrentDirectory()
	if err != nil {
		return err
	}
	server, err := localserver.New(
		repository,
		appID,
		githubstatus.GHRunner{},
		passportrepo.ExecRunner{},
	)
	if err != nil {
		return err
	}
	target, reused, err := server.Start()
	if err != nil {
		return err
	}
	fmt.Println("Local-only passport:", target)
	if reused {
		fmt.Println("Reusing the passport process already running for this clone.")
		if !noBrowser && os.Getenv("PASSPORT_NO_BROWSER") != "1" {
			return localserver.OpenBrowser(target)
		}
		return nil
	}
	fmt.Println("Press Ctrl+C in this terminal to stop the local passport.")
	if !noBrowser && os.Getenv("PASSPORT_NO_BROWSER") != "1" {
		if err := localserver.OpenBrowser(target); err != nil {
			_ = server.Close(context.Background())
			return err
		}
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	<-signals
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Close(shutdown)
}

func status(arguments []string) error {
	jsonOutput, err := onlyJSONOption(arguments)
	if err != nil {
		return err
	}
	repository, _, err := contextForCurrentDirectory()
	if err != nil {
		return err
	}
	state, stateErr := localstate.New(repository.Root).Load()
	value := map[string]any{
		"source":             "local_navigation_only",
		"curriculum_version": repository.Passport.CurriculumVersion,
		"branch":             repository.Branch,
		"local_head_sha":     repository.HeadSHA,
		"remote_head_sha":    repository.RemoteHeadSHA,
		"dirty":              repository.Dirty,
		"local_state":        state,
	}
	if stateErr != nil {
		value["local_state_error"] = stateErr.Error()
	}
	if jsonOutput {
		return writeJSON(value)
	}
	fmt.Printf("Curriculum: %s\n", repository.Passport.CurriculumVersion)
	fmt.Printf("Branch: %s\n", repository.Branch)
	fmt.Printf("Local worktree: %s\n", map[bool]string{true: "uncommitted work present", false: "clean"}[repository.Dirty])
	if state.LastOpenedMission != "" {
		fmt.Printf("Local resume mission: %s (navigation only)\n", state.LastOpenedMission)
	}
	fmt.Println("Run `gh passport sync` for controller-verified GitHub status.")
	return nil
}

func syncStatus(arguments []string) error {
	jsonOutput, err := onlyJSONOption(arguments)
	if err != nil {
		return err
	}
	repository, appID, err := contextForCurrentDirectory()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := githubstatus.Sync(ctx, repository, appID, githubstatus.GHRunner{})
	if err != nil {
		return err
	}
	store := localstate.New(repository.Root)
	if result.Official != nil {
		if err := store.SaveStatus(result.Official); err != nil {
			return err
		}
	}
	state, _ := store.Load()
	if state.SchemaVersion != 0 {
		state.LastOfficialSync = result.SyncedAt
		state.LastSeenHeadSHA = result.RemoteHeadSHA
		if err := store.Save(state); err != nil {
			return err
		}
	}
	if jsonOutput {
		return writeJSON(result)
	}
	switch result.State {
	case githubstatus.StateVerified:
		official := result.Official
		fmt.Printf("Official stage: %s\n", official.Status.Stage)
		fmt.Printf("Verified head: %s\n", official.RemoteHeadSHA)
		fmt.Printf("Source: controller App %d, Check Run %d\n", official.ControllerAppID, official.CheckRunID)
		fmt.Printf("Assessment PR: %s\n", official.PullRequestURL)
	case githubstatus.StateWaitingForController:
		fmt.Println("Submitted to GitHub; waiting for the scheduled automatic check.")
		fmt.Println("Do not push the same change again. Sync again later.")
		fmt.Println("If no check appears after 30 minutes, submit one public, non-secret help request:")
		fmt.Println(result.RecoveryIssueURL)
		fmt.Printf("Assessment PR: %s\n", result.PullRequestURL)
	case githubstatus.StateRecoveryRequired:
		fmt.Println("The permanent assessment pull request is not open. Your Git work was not changed.")
		fmt.Println("From this clean passport folder, run `gh passport start` to create or find the draft PR.")
		fmt.Printf("If that stops or the PR was merged, request safe recovery: %s\n", result.RecoveryIssueURL)
	default:
		return fmt.Errorf("unsupported sync state %q", result.State)
	}
	return nil
}

func doctor(arguments []string) error {
	options, err := parseDoctorOptions(arguments)
	if err != nil {
		return err
	}
	checks := []doctorCheck{}
	for _, tool := range []string{"git", "gh"} {
		_, lookupErr := exec.LookPath(tool)
		status := "ok"
		detail := "available"
		if lookupErr != nil {
			status, detail = "blocked", "not found on PATH"
		}
		checks = append(checks, doctorCheck{tool, status, detail})
	}
	auth := exec.Command("gh", "auth", "status", "--hostname", "github.com")
	auth.Env = append(os.Environ(), "GH_PAGER=cat", "NO_COLOR=1")
	if err := auth.Run(); err != nil {
		checks = append(checks, doctorCheck{"github-auth", "blocked", "run gh auth login --web --git-protocol https"})
	} else {
		checks = append(checks, doctorCheck{"github-auth", "ok", "authenticated through GitHub CLI"})
	}
	repository, appID, repositoryErr := contextForCurrentDirectory()
	localStateIgnored := false
	if repositoryErr != nil {
		checks = append(checks, doctorCheck{"passport-repository", "blocked", repositoryErr.Error()})
	} else {
		checks = append(checks, doctorCheck{"passport-repository", "ok", repository.Owner + "/" + repository.Name})
		localStateIgnored = passportrepo.IsLocalStateIgnored(repository, passportrepo.ExecRunner{})
		if localStateIgnored {
			checks = append(checks, doctorCheck{"local-state-ignore", "ok", ".passport-local is ignored"})
		} else {
			checks = append(checks, doctorCheck{"local-state-ignore", "blocked", "add /.passport-local/ to the generated passport .gitignore"})
		}
		if appID > 0 {
			checks = append(checks, doctorCheck{"controller-app", "ok", strconv.FormatInt(appID, 10)})
		} else {
			checks = append(checks, doctorCheck{"controller-app", "blocked", "launcher release has no controller App ID"})
		}
	}
	bundleCreated := false
	if options.Bundle {
		if repositoryErr != nil {
			checks = append(checks, doctorCheck{"diagnostic-bundle", "blocked", "run from the generated passport clone"})
		} else if !localStateIgnored {
			checks = append(checks, doctorCheck{"diagnostic-bundle", "blocked", "refusing to write until .passport-local is gitignored"})
		} else {
			path, bundleErr := writeDiagnosticBundle(repository.Root, checks)
			if bundleErr != nil {
				checks = append(checks, doctorCheck{"diagnostic-bundle", "blocked", bundleErr.Error()})
			} else {
				bundleCreated = true
				relative := filepath.ToSlash(filepath.Join(".passport-local", "diagnostics", filepath.Base(path)))
				checks = append(checks, doctorCheck{"diagnostic-bundle", "ok", relative})
			}
		}
	}
	if options.JSON {
		return writeJSON(map[string]any{
			"checks":                    checks,
			"mutated_files":             bundleCreated,
			"diagnostic_bundle_created": bundleCreated,
		})
	}
	blocked := false
	for _, item := range checks {
		fmt.Printf("%-22s %-8s %s\n", item.Name, item.Status, item.Detail)
		blocked = blocked || item.Status == "blocked"
	}
	if blocked {
		return errors.New("one or more read-only diagnostics are blocked")
	}
	return nil
}

func parseDoctorOptions(arguments []string) (doctorOptions, error) {
	var options doctorOptions
	for _, argument := range arguments {
		switch argument {
		case "--json":
			if options.JSON {
				return doctorOptions{}, errors.New("--json may be supplied only once")
			}
			options.JSON = true
		case "--bundle":
			if options.Bundle {
				return doctorOptions{}, errors.New("--bundle may be supplied only once")
			}
			options.Bundle = true
		default:
			return doctorOptions{}, fmt.Errorf("unsupported doctor option %q", argument)
		}
	}
	return options, nil
}

func writeDiagnosticBundle(repositoryRoot string, checks []doctorCheck) (string, error) {
	localDirectory := filepath.Join(repositoryRoot, ".passport-local")
	diagnosticDirectory := filepath.Join(localDirectory, "diagnostics")
	for _, directory := range []string{localDirectory, diagnosticDirectory} {
		info, err := os.Lstat(directory)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(directory, 0o700); err != nil {
				return "", fmt.Errorf("create private diagnostic directory: %w", err)
			}
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect private diagnostic directory: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("diagnostic directory must be a real private directory")
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return "", fmt.Errorf("protect diagnostic directory: %w", err)
		}
	}

	sanitized := make([]diagnosticCheck, 0, len(checks))
	for _, item := range checks {
		sanitized = append(sanitized, diagnosticCheck{Name: item.Name, Status: item.Status})
	}
	report := diagnosticReport{
		SchemaVersion:     1,
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		LauncherVersion:   version,
		CurriculumVersion: curriculumVersion,
		OperatingSystem:   runtime.GOOS,
		Architecture:      runtime.GOARCH,
		Checks:            sanitized,
		Telemetry:         "none",
	}
	for attempt := 0; attempt < 3; attempt++ {
		name := fmt.Sprintf("doctor-%s-%d.json", time.Now().UTC().Format("20060102T150405.000000000Z"), attempt)
		path := filepath.Join(diagnosticDirectory, name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("create diagnostic bundle: %w", err)
		}
		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "  ")
		writeErr := encoder.Encode(report)
		if syncErr := file.Sync(); writeErr == nil {
			writeErr = syncErr
		}
		if closeErr := file.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			_ = os.Remove(path)
			return "", fmt.Errorf("write diagnostic bundle: %w", writeErr)
		}
		return path, nil
	}
	return "", errors.New("could not allocate a unique diagnostic bundle")
}

func contextForCurrentDirectory() (passportrepo.Repository, int64, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return passportrepo.Repository{}, 0, err
	}
	repository, err := passportrepo.Find(workingDirectory, passportrepo.ExecRunner{})
	if err != nil {
		return passportrepo.Repository{}, 0, err
	}
	if repository.Passport.CurriculumVersion != curriculumVersion {
		return passportrepo.Repository{}, 0, fmt.Errorf(
			"passport curriculum %s requires a compatible launcher; this launcher embeds %s",
			repository.Passport.CurriculumVersion, curriculumVersion,
		)
	}
	appID, err := strconv.ParseInt(controllerAppID, 10, 64)
	if err != nil || appID < 0 {
		return passportrepo.Repository{}, 0, errors.New("launcher controller App ID is invalid")
	}
	return repository, appID, nil
}

func onlyJSONOption(arguments []string) (bool, error) {
	if len(arguments) == 0 {
		return false, nil
	}
	if len(arguments) == 1 && arguments[0] == "--json" {
		return true, nil
	}
	return false, fmt.Errorf("unsupported options: %s", strings.Join(arguments, " "))
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func usage() {
	fmt.Print(`IDEAL Lab local-first IT Passport

Usage:
  gh passport start [--directory PATH] [--platform OS] [--responsibility ID] [--yes] [--no-browser]
  gh passport open [--no-browser]
  gh passport status [--json]
  gh passport sync [--json]
  gh passport doctor [--json] [--bundle]
  gh passport version

The local application remembers navigation only. GitHub controller checks and
the assigned reviewer determine official completion. doctor --bundle writes
only redacted diagnostics under the gitignored .passport-local directory.
`)
}
