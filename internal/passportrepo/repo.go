package passportrepo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maxPassportBytes = 1 << 20

const (
	SourceOwner = "soheylm-passport-sandbox"
	SourceName  = "passport-exercises"
)

var (
	missionIDPattern  = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	githubPartPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	githubUserPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
)

type Passport struct {
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

type Repository struct {
	Root          string   `json:"-"`
	Owner         string   `json:"owner"`
	Name          string   `json:"name"`
	UpstreamOwner string   `json:"upstream_owner"`
	UpstreamName  string   `json:"upstream_name"`
	Branch        string   `json:"branch"`
	HeadSHA       string   `json:"head_sha"`
	RemoteHeadSHA string   `json:"remote_head_sha,omitempty"`
	Dirty         bool     `json:"dirty"`
	Passport      Passport `json:"passport"`
}

type CommandRunner interface {
	Run(ctx context.Context, directory string, name string, args ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, directory string, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return command.Output()
}

func Find(start string, runner CommandRunner) (Repository, error) {
	root, err := findRoot(start)
	if err != nil {
		return Repository{}, err
	}
	passport, err := loadPassport(filepath.Join(root, "passport.json"))
	if err != nil {
		return Repository{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	remote, err := runTrimmed(ctx, runner, root, "git", "remote", "get-url", "origin")
	if err != nil {
		return Repository{}, fmt.Errorf("read Git origin: %w", err)
	}
	owner, name, err := parseGitHubRemote(remote)
	if err != nil {
		return Repository{}, err
	}
	if !strings.EqualFold(owner, passport.GitHubUser) || name != SourceName {
		return Repository{}, fmt.Errorf(
			"Git origin must be the learner fork %s/%s, not %s/%s",
			passport.GitHubUser,
			SourceName,
			owner,
			name,
		)
	}
	upstream, err := runTrimmed(ctx, runner, root, "git", "remote", "get-url", "upstream")
	if err != nil {
		return Repository{}, errors.New("passport is missing the official `upstream` remote; run `gh passport doctor` for recovery")
	}
	upstreamOwner, upstreamName, err := parseGitHubRemote(upstream)
	if err != nil || upstreamOwner != SourceOwner || upstreamName != SourceName {
		return Repository{}, errors.New("passport upstream must be soheylm-passport-sandbox/passport-exercises")
	}
	branch, err := runTrimmed(ctx, runner, root, "git", "branch", "--show-current")
	if err != nil || branch == "" {
		return Repository{}, errors.New("passport must be opened from a named Git branch")
	}
	if branch != passport.AssessmentBranch {
		return Repository{}, fmt.Errorf(
			"passport is on branch %q; switch safely with `git switch %s`",
			branch,
			passport.AssessmentBranch,
		)
	}
	head, err := runTrimmed(ctx, runner, root, "git", "rev-parse", "HEAD")
	if err != nil || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(head) {
		return Repository{}, errors.New("passport Git HEAD is unavailable or invalid")
	}
	status, err := runner.Run(ctx, root, "git", "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return Repository{}, fmt.Errorf("inspect Git worktree: %w", err)
	}
	remoteHead, _ := runTrimmed(ctx, runner, root, "git", "rev-parse", "--verify", "origin/"+branch)
	return Repository{
		Root:          root,
		Owner:         owner,
		Name:          name,
		UpstreamOwner: upstreamOwner,
		UpstreamName:  upstreamName,
		Branch:        branch,
		HeadSHA:       head,
		RemoteHeadSHA: remoteHead,
		Dirty:         len(bytes.TrimSpace(status)) > 0,
		Passport:      passport,
	}, nil
}

func IsLocalStateIgnored(repository Repository, runner CommandRunner) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := runner.Run(
		ctx,
		repository.Root,
		"git",
		"check-ignore",
		"--quiet",
		".passport-local/state.json",
	)
	return err == nil
}

func findRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		passport := filepath.Join(current, "passport.json")
		git := filepath.Join(current, ".git")
		if regularFile(passport) && exists(git) {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("not inside a generated IDEAL Lab passport repository")
		}
		current = parent
	}
}

func loadPassport(path string) (Passport, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Passport{}, fmt.Errorf("read passport.json: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxPassportBytes {
		return Passport{}, errors.New("passport.json is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return Passport{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var passport Passport
	if err := decoder.Decode(&passport); err != nil {
		return Passport{}, fmt.Errorf("parse passport.json: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Passport{}, errors.New("passport.json contains trailing JSON")
	}
	if passport.SchemaVersion != 2 || passport.CurriculumVersion == "" {
		return Passport{}, errors.New("passport.json has an unsupported schema or curriculum")
	}
	expectedBranch := "onboarding/" + strings.ToLower(passport.GitHubUser)
	if !githubUserPattern.MatchString(passport.GitHubUser) || passport.AssessmentBranch != expectedBranch {
		return Passport{}, errors.New("passport.json has an invalid assessment branch")
	}
	if passport.SourceRepository != SourceOwner+"/"+SourceName ||
		!strings.EqualFold(passport.ForkRepository, passport.GitHubUser+"/"+SourceName) {
		return Passport{}, errors.New("passport.json does not identify the official exercise repository and learner fork")
	}
	if passport.Platform != "windows" && passport.Platform != "macos" && passport.Platform != "linux" {
		return Passport{}, errors.New("passport.json has an unsupported platform")
	}
	if len(passport.Missions) == 0 {
		return Passport{}, errors.New("passport.json contains no assigned missions")
	}
	seen := make(map[string]bool, len(passport.Missions))
	for _, mission := range passport.Missions {
		if !missionIDPattern.MatchString(mission) || seen[mission] {
			return Passport{}, errors.New("passport.json contains invalid or duplicate missions")
		}
		seen[mission] = true
	}
	return passport, nil
}

func parseGitHubRemote(value string) (string, string, error) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(value), ".git")
	var path string
	switch {
	case strings.HasPrefix(trimmed, "https://github.com/"):
		path = strings.TrimPrefix(trimmed, "https://github.com/")
	case strings.HasPrefix(trimmed, "git@github.com:"):
		path = strings.TrimPrefix(trimmed, "git@github.com:")
	case strings.HasPrefix(trimmed, "ssh://git@github.com/"):
		path = strings.TrimPrefix(trimmed, "ssh://git@github.com/")
	default:
		return "", "", errors.New("Git origin must be an HTTPS or SSH github.com repository")
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 || !githubPartPattern.MatchString(parts[0]) || !githubPartPattern.MatchString(parts[1]) {
		return "", "", errors.New("GitHub origin owner or repository is invalid")
	}
	return parts[0], parts[1], nil
}

func runTrimmed(ctx context.Context, runner CommandRunner, directory, name string, args ...string) (string, error) {
	value, err := runner.Run(ctx, directory, name, args...)
	return strings.TrimSpace(string(value)), err
}

func regularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
