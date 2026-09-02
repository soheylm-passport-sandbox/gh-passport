package starter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/deployment"
)

const sourceRepository = deployment.ExerciseRepository

const (
	commandTimeout      = 2 * time.Minute
	forkReadyAttempts   = 15
	forkReadyRetryDelay = 2 * time.Second
)

type Options struct {
	Directory        string
	Platform         string
	Responsibilities []string
	AssumeYes        bool
	Input            io.Reader
	Output           io.Writer
}

type Result struct {
	Directory      string
	PullRequestURL string
}

type commandRunner interface {
	Run(ctx context.Context, directory string, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, directory string, name string, args ...string) ([]byte, error) {
	commandContext, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	command := exec.CommandContext(commandContext, name, args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GH_PAGER=cat", "NO_COLOR=1", "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return nil, fmt.Errorf("%s: %s", strings.Join(append([]string{name}, args...), " "), message)
		}
		return nil, err
	}
	return output, nil
}

type catalog struct {
	SchemaVersion     int                       `json:"schema_version"`
	CurriculumVersion string                    `json:"curriculum_version"`
	SourceRepository  string                    `json:"source_repository"`
	Platforms         []string                  `json:"platforms"`
	UniversalTracks   []string                  `json:"universal_tracks"`
	TrackDependencies map[string][]string       `json:"track_dependencies"`
	Responsibilities  map[string]responsibility `json:"responsibilities"`
	Tracks            []track                   `json:"tracks"`
	Missions          map[string]mission        `json:"missions"`
}

type responsibility struct {
	Title  string   `json:"title"`
	Tracks []string `json:"tracks"`
}

type track struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Missions []string `json:"missions"`
}

type mission struct {
	Track             string   `json:"track"`
	Evidence          []string `json:"evidence"`
	EditableArtifacts []string `json:"editable_artifacts"`
	HumanGate         bool     `json:"human_gate"`
}

type passport struct {
	SchemaVersion     int      `json:"schema_version"`
	CurriculumVersion string   `json:"curriculum_version"`
	GitHubUser        string   `json:"github_user"`
	SourceRepository  string   `json:"source_repository"`
	ForkRepository    string   `json:"fork_repository"`
	AssessmentBranch  string   `json:"assessment_branch"`
	Platform          string   `json:"platform"`
	Responsibilities  []string `json:"responsibilities"`
	Missions          []string `json:"missions"`
}

func Run(options Options) (Result, error) {
	return run(options, execRunner{})
}

func run(options Options, runner commandRunner) (Result, error) {
	if options.Input == nil {
		options.Input = os.Stdin
	}
	if options.Output == nil {
		options.Output = os.Stdout
	}
	// The overall start journey includes human prompts and browser authentication.
	// Individual network and Git operations retain their own shorter timeouts.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if _, err := runner.Run(ctx, "", "git", "--version"); err != nil {
		return Result{}, errors.New("Git is not available; install Git, close and reopen this terminal, then retry `gh passport start`")
	}

	usernameRaw, err := runner.Run(ctx, "", "gh", "api", "user", "--jq", ".login")
	if err != nil {
		return Result{}, errors.New("GitHub CLI is not authenticated; run `gh auth login --web --git-protocol https`, then retry")
	}
	username := strings.TrimSpace(string(usernameRaw))
	if username == "" || strings.ContainsAny(username, " /\\") {
		return Result{}, errors.New("GitHub returned an invalid account name")
	}
	if _, err := runner.Run(ctx, "", "gh", "auth", "setup-git"); err != nil {
		return Result{}, fmt.Errorf("configure Git credential helper: %w", err)
	}

	directory, err := destination(options.Directory)
	if err != nil {
		return Result{}, err
	}
	if err := ensureForkAndClone(ctx, runner, username, directory); err != nil {
		return Result{}, err
	}
	if err := ensureRemotes(ctx, runner, username, directory); err != nil {
		return Result{}, err
	}
	status, err := runner.Run(ctx, directory, "git", "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return Result{}, fmt.Errorf("inspect passport working tree: %w", err)
	}
	if strings.TrimSpace(string(status)) != "" {
		return Result{}, errors.New("passport folder has local changes; keep them safe, then run `gh passport open` instead of restarting")
	}

	catalogRaw, err := runner.Run(
		ctx, directory, "git", "show", "upstream/main:passport-curriculum.json",
	)
	if err != nil {
		return Result{}, fmt.Errorf("read official passport curriculum catalogue: %w", err)
	}
	catalogValue, err := decodeCatalog(catalogRaw)
	if err != nil {
		return Result{}, err
	}
	branch := "onboarding/" + strings.ToLower(username)
	if err := prepareBranch(ctx, runner, directory, branch); err != nil {
		return Result{}, err
	}
	platform := ""
	responsibilities := []string(nil)
	missions := []string(nil)
	existing, err := loadExistingPassport(filepath.Join(directory, "passport.json"), catalogValue, username, branch)
	if err != nil {
		return Result{}, err
	}
	if existing != nil {
		platform = existing.Platform
		responsibilities = existing.Responsibilities
		missions = existing.Missions
		fmt.Fprintln(options.Output, "Reusing the existing passport route in this folder.")
	} else {
		platform, err = choosePlatform(options.Platform, options.Input, options.Output)
		if err != nil {
			return Result{}, err
		}
		responsibilities, err = chooseResponsibilities(catalogValue, options, options.Input, options.Output)
		if err != nil {
			return Result{}, err
		}
		missions, err = resolveMissions(catalogValue, responsibilities)
		if err != nil {
			return Result{}, err
		}
	}
	value := passport{
		SchemaVersion:     2,
		CurriculumVersion: catalogValue.CurriculumVersion,
		GitHubUser:        username,
		SourceRepository:  sourceRepository,
		ForkRepository:    username + "/" + deployment.ExerciseName,
		AssessmentBranch:  branch,
		Platform:          platform,
		Responsibilities:  responsibilities,
		Missions:          missions,
	}
	changed, err := writePassport(filepath.Join(directory, "passport.json"), value)
	if err != nil {
		return Result{}, err
	}
	if changed {
		if _, err := runner.Run(ctx, directory, "git", "add", "--", "passport.json"); err != nil {
			return Result{}, err
		}
		if _, err := runner.Run(ctx, directory, "git", "commit", "-m", "chore(passport): start assigned learning route"); err != nil {
			return Result{}, err
		}
	}
	if _, err := runner.Run(ctx, directory, "git", "push", "--set-upstream", "origin", branch); err != nil {
		return Result{}, fmt.Errorf("publish assessment branch: %w", err)
	}
	prURL, err := ensurePullRequest(ctx, runner, username, branch, directory)
	if err != nil {
		return Result{}, err
	}
	fmt.Fprintf(options.Output, "\nPassport ready.\nFolder: %s\nAssessment PR: %s\n", directory, prURL)
	return Result{Directory: directory, PullRequestURL: prURL}, nil
}

func destination(raw string) (string, error) {
	if raw == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("cannot locate the user home directory; pass --directory")
		}
		raw = filepath.Join(home, "IDEAL-passport")
	}
	value, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve passport folder: %w", err)
	}
	return value, nil
}

func ensureForkAndClone(ctx context.Context, runner commandRunner, username, directory string) error {
	if info, err := os.Stat(directory); err == nil {
		if !info.IsDir() {
			return errors.New("passport destination exists but is not a folder")
		}
		if _, err := os.Stat(filepath.Join(directory, ".git")); err != nil {
			return errors.New("passport destination already exists and is not a Git clone; choose another --directory")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect passport destination: %w", err)
	}
	forkCreated := false
	if _, err := runner.Run(ctx, "", "gh", "repo", "view", username+"/"+deployment.ExerciseName, "--json", "nameWithOwner"); err != nil {
		if _, err := runner.Run(ctx, "", "gh", "repo", "fork", sourceRepository, "--clone=false"); err != nil {
			return fmt.Errorf("create personal exercise fork: %w", err)
		}
		forkCreated = true
	} else if err := verifyDirectFork(ctx, runner, username); err != nil {
		return err
	}
	if forkCreated {
		var availableErr error
		for attempt := 0; attempt < forkReadyAttempts; attempt++ {
			if _, availableErr = runner.Run(ctx, "", "gh", "repo", "view", username+"/"+deployment.ExerciseName, "--json", "nameWithOwner"); availableErr == nil {
				break
			}
			if attempt+1 < forkReadyAttempts {
				select {
				case <-ctx.Done():
					return fmt.Errorf("wait for personal exercise fork: %w", ctx.Err())
				case <-time.After(forkReadyRetryDelay):
				}
			}
		}
		if availableErr != nil {
			return fmt.Errorf("personal exercise fork was not ready after waiting for GitHub: %w", availableErr)
		}
		if err := verifyDirectFork(ctx, runner, username); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(directory), 0o755); err != nil {
		return fmt.Errorf("create passport parent folder: %w", err)
	}
	if _, err := runner.Run(ctx, "", "git", "clone", "https://github.com/"+username+"/"+deployment.ExerciseName+".git", directory); err != nil {
		return fmt.Errorf("clone personal exercise fork: %w", err)
	}
	return nil
}

func verifyDirectFork(ctx context.Context, runner commandRunner, username string) error {
	raw, err := runner.Run(
		ctx,
		"",
		"gh",
		"api",
		"repos/"+username+"/"+deployment.ExerciseName,
		"--jq",
		`[.fork, .parent.full_name] | @tsv`,
	)
	if err != nil {
		return fmt.Errorf("verify personal exercise fork: %w", err)
	}
	if strings.TrimSpace(string(raw)) != "true\t"+sourceRepository {
		return fmt.Errorf("username/%s already exists but is not the official direct fork; it was left unchanged, request safe help", deployment.ExerciseName)
	}
	return nil
}

func ensureRemotes(ctx context.Context, runner commandRunner, username, directory string) error {
	originRaw, err := runner.Run(ctx, directory, "git", "remote", "get-url", "origin")
	originRepository, validOrigin := githubRepository(string(originRaw))
	if err != nil || !validOrigin || !strings.EqualFold(originRepository, username+"/"+deployment.ExerciseName) {
		return fmt.Errorf("existing folder does not use the expected personal %s fork as origin", deployment.ExerciseName)
	}
	upstreamURL := "https://github.com/" + sourceRepository + ".git"
	if _, err := runner.Run(ctx, directory, "git", "remote", "get-url", "upstream"); err != nil {
		if _, err := runner.Run(ctx, directory, "git", "remote", "add", "upstream", upstreamURL); err != nil {
			return fmt.Errorf("add official upstream remote: %w", err)
		}
	} else if _, err := runner.Run(ctx, directory, "git", "remote", "set-url", "upstream", upstreamURL); err != nil {
		return fmt.Errorf("repair official upstream remote: %w", err)
	}
	if _, err := runner.Run(ctx, directory, "git", "fetch", "--prune", "origin"); err != nil {
		return fmt.Errorf("refresh personal exercise fork: %w", err)
	}
	if _, err := runner.Run(ctx, directory, "git", "fetch", "upstream", "main"); err != nil {
		return fmt.Errorf("fetch official exercises: %w", err)
	}
	return nil
}

func githubRepository(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "git@github.com:") {
		value = strings.TrimPrefix(value, "git@github.com:")
	} else {
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "ssh") || !strings.EqualFold(parsed.Hostname(), "github.com") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", false
		}
		if parsed.User != nil && parsed.User.Username() != "git" {
			return "", false
		}
		value = strings.TrimPrefix(parsed.Path, "/")
	}
	value = strings.TrimSuffix(value, ".git")
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(value, "?#\\") {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

func choosePlatform(value string, input io.Reader, output io.Writer) (string, error) {
	if value == "" {
		switch runtime.GOOS {
		case "windows", "darwin", "linux":
			value = map[string]string{"darwin": "macos"}[runtime.GOOS]
			if value == "" {
				value = runtime.GOOS
			}
		default:
			fmt.Fprint(output, "Platform (windows, macos, or linux): ")
			line, _ := bufio.NewReader(input).ReadString('\n')
			value = strings.ToLower(strings.TrimSpace(line))
		}
	}
	if value != "windows" && value != "macos" && value != "linux" {
		return "", errors.New("platform must be windows, macos, or linux")
	}
	return value, nil
}

func chooseResponsibilities(value catalog, options Options, input io.Reader, output io.Writer) ([]string, error) {
	selected := append([]string(nil), options.Responsibilities...)
	if len(selected) == 0 && !options.AssumeYes {
		reader := bufio.NewReader(input)
		keys := make([]string, 0, len(value.Responsibilities))
		for key := range value.Responsibilities {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		fmt.Fprintln(output, "\nSelect every activity you expect to do. Answer y or n.")
		for _, key := range keys {
			fmt.Fprintf(output, "%s? [y/N]: ", value.Responsibilities[key].Title)
			line, _ := reader.ReadString('\n')
			answer := strings.ToLower(strings.TrimSpace(line))
			if answer == "y" || answer == "yes" {
				selected = append(selected, key)
			}
		}
	}
	seen := map[string]bool{}
	clean := make([]string, 0, len(selected))
	for _, key := range selected {
		if _, ok := value.Responsibilities[key]; !ok {
			return nil, fmt.Errorf("unknown responsibility %q", key)
		}
		if !seen[key] {
			seen[key] = true
			clean = append(clean, key)
		}
	}
	sort.Strings(clean)
	return clean, nil
}

func resolveMissions(value catalog, responsibilities []string) ([]string, error) {
	selected := map[string]bool{}
	var addTrack func(string) error
	addTrack = func(id string) error {
		if selected[id] {
			return nil
		}
		if _, ok := value.TrackDependencies[id]; !ok {
			return fmt.Errorf("catalog references unknown track %q", id)
		}
		for _, dependency := range value.TrackDependencies[id] {
			if err := addTrack(dependency); err != nil {
				return err
			}
		}
		selected[id] = true
		return nil
	}
	for _, id := range value.UniversalTracks {
		if err := addTrack(id); err != nil {
			return nil, err
		}
	}
	for _, key := range responsibilities {
		for _, id := range value.Responsibilities[key].Tracks {
			if err := addTrack(id); err != nil {
				return nil, err
			}
		}
	}
	missions := []string{}
	for _, track := range value.Tracks {
		if selected[track.ID] {
			missions = append(missions, track.Missions...)
		}
	}
	if len(missions) == 0 {
		return nil, errors.New("catalog resolved no missions")
	}
	return missions, nil
}

func loadCatalog(path string) (catalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return catalog{}, fmt.Errorf("read passport curriculum catalogue: %w", err)
	}
	return decodeCatalog(raw)
}

func decodeCatalog(raw []byte) (catalog, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value catalog
	if err := decoder.Decode(&value); err != nil {
		return catalog{}, fmt.Errorf("parse passport curriculum catalogue: %w", err)
	}
	if value.SchemaVersion != 1 || value.SourceRepository != sourceRepository || value.CurriculumVersion == "" {
		return catalog{}, errors.New("passport curriculum catalogue has an unsupported identity")
	}
	return value, nil
}

func prepareBranch(ctx context.Context, runner commandRunner, directory, branch string) error {
	_, localErr := runner.Run(
		ctx, directory, "git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch,
	)
	_, remoteErr := runner.Run(
		ctx, directory, "git", "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch,
	)
	if localErr == nil {
		if _, err := runner.Run(ctx, directory, "git", "switch", branch); err != nil {
			return fmt.Errorf("switch to existing assessment branch: %w", err)
		}
		if remoteErr == nil {
			if _, err := runner.Run(ctx, directory, "git", "merge", "--ff-only", "origin/"+branch); err != nil {
				return errors.New("local and remote assessment branches diverged; no files were reset, ask for safe recovery")
			}
		}
		return nil
	}
	if remoteErr == nil {
		if _, err := runner.Run(
			ctx, directory, "git", "switch", "--create", branch, "--track", "origin/"+branch,
		); err != nil {
			return fmt.Errorf("resume remote assessment branch: %w", err)
		}
		return nil
	}
	if _, err := runner.Run(ctx, directory, "git", "switch", "--create", branch, "upstream/main"); err != nil {
		return fmt.Errorf("create assessment branch: %w", err)
	}
	return nil
}

func writePassport(path string, value passport) (bool, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return false, err
	}
	raw = append(raw, '\n')
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, raw) {
			return false, nil
		}
		return false, errors.New("passport.json already exists with a different route; do not overwrite it automatically")
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("inspect passport.json: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return false, fmt.Errorf("write passport.json: %w", err)
	}
	return true, nil
}

func loadExistingPassport(path string, value catalog, username, branch string) (*passport, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read existing passport.json: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var existing passport
	if err := decoder.Decode(&existing); err != nil {
		return nil, fmt.Errorf("parse existing passport.json: %w", err)
	}
	if existing.SchemaVersion != 2 || existing.CurriculumVersion != value.CurriculumVersion ||
		!strings.EqualFold(existing.GitHubUser, username) || existing.SourceRepository != sourceRepository ||
		!strings.EqualFold(existing.ForkRepository, username+"/"+deployment.ExerciseName) ||
		existing.AssessmentBranch != branch {
		return nil, errors.New("existing passport.json belongs to a different identity, release, or branch; do not overwrite it")
	}
	if existing.Platform != "windows" && existing.Platform != "macos" && existing.Platform != "linux" {
		return nil, errors.New("existing passport.json has an unsupported platform")
	}
	for _, key := range existing.Responsibilities {
		if _, ok := value.Responsibilities[key]; !ok {
			return nil, fmt.Errorf("existing passport.json has unknown responsibility %q", key)
		}
	}
	expected, err := resolveMissions(value, existing.Responsibilities)
	if err != nil || !sameStrings(expected, existing.Missions) {
		return nil, errors.New("existing passport.json mission route is not canonical; do not overwrite it")
	}
	return &existing, nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func ensurePullRequest(ctx context.Context, runner commandRunner, username, branch, directory string) (string, error) {
	existing, err := findPullRequest(ctx, runner, username, branch, directory)
	if err != nil {
		return "", fmt.Errorf("query permanent assessment pull request: %w", err)
	}
	if existing != "" {
		return existing, nil
	}
	body := "This is my permanent public IT Passport assessment PR.\n\n" +
		"I will submit only fictional exercise content and sanitized evidence. " +
		"Apart from the GitHub identity already visible on this PR, I will never include credentials, " +
		"ETH or other private identifiers, private logs, or research data.\n\n" +
		"The automatic controller checks each pushed commit. I will not merge or close this PR.\n"
	raw, err := runner.Run(
		ctx, directory, "gh", "pr", "create", "--repo", sourceRepository,
		"--head", username+":"+branch, "--base", "main", "--draft",
		"--title", "chore(passport): complete "+username+" onboarding route",
		"--body", body,
	)
	if err != nil {
		// A concurrent start may have created the permanent PR after our lookup.
		if existing, lookupErr := findPullRequest(ctx, runner, username, branch, directory); lookupErr == nil && existing != "" {
			return existing, nil
		}
		return "", fmt.Errorf("create permanent assessment pull request: %w", err)
	}
	url := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(url, deployment.PullURLPrefix) {
		return "", errors.New("GitHub did not return the expected central assessment pull request URL")
	}
	return url, nil
}

func findPullRequest(ctx context.Context, runner commandRunner, username, branch, directory string) (string, error) {
	path := fmt.Sprintf(
		"repos/%s/pulls?state=open&head=%s&base=main&per_page=2",
		sourceRepository,
		url.QueryEscape(username+":"+branch),
	)
	raw, err := runner.Run(ctx, directory, "gh", "api", path, "--jq", ".[0].html_url")
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(raw))
	if value != "" && !strings.HasPrefix(value, deployment.PullURLPrefix) {
		return "", errors.New("GitHub returned an unexpected assessment pull request URL")
	}
	return value, nil
}
