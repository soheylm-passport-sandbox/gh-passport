package githubstatus

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/passportrepo"
)

const checkName = "IDEAL Lab IT Passport"

const (
	StateVerified             = "verified"
	StateWaitingForController = "waiting_for_controller"
	StateRecoveryRequired     = "assessment_recovery_required"
)

var markerPattern = regexp.MustCompile(`<!-- ideal-passport-status:v2:([A-Za-z0-9_-]+) -->`)

type Runner interface {
	Run(ctx context.Context, directory string, args ...string) ([]byte, error)
}

type GHRunner struct{}

func (GHRunner) Run(ctx context.Context, directory string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "gh", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GH_PAGER=cat", "NO_COLOR=1")
	output, err := command.Output()
	if err == nil {
		return output, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		message := strings.TrimSpace(string(exit.Stderr))
		if message != "" {
			return nil, errors.New(message)
		}
	}
	return nil, err
}

type Mission struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type ControllerStatus struct {
	SchemaVersion     int       `json:"schema_version"`
	CurriculumVersion string    `json:"curriculum_version"`
	HeadSHA           string    `json:"head_sha"`
	CurrentMission    *string   `json:"current_mission"`
	Missions          []Mission `json:"missions"`
	Stage             string    `json:"stage"`
	ReviewState       string    `json:"review_state"`
	AssessedAt        string    `json:"assessed_at"`
}

type Official struct {
	Source            string           `json:"source"`
	ControllerAppID   int64            `json:"controller_app_id"`
	CheckRunID        int64            `json:"check_run_id"`
	CheckRunURL       string           `json:"check_run_url"`
	CheckConclusion   string           `json:"check_conclusion"`
	PullRequestNumber int              `json:"pull_request_number"`
	PullRequestURL    string           `json:"pull_request_url"`
	RepositoryURL     string           `json:"repository_url"`
	RecoveryIssueURL  string           `json:"recovery_issue_url"`
	RemoteHeadSHA     string           `json:"remote_head_sha"`
	Status            ControllerStatus `json:"status"`
	SyncedAt          string           `json:"synced_at"`
}

type Result struct {
	State             string    `json:"state"`
	Official          *Official `json:"official,omitempty"`
	PullRequestNumber int       `json:"pull_request_number,omitempty"`
	PullRequestURL    string    `json:"pull_request_url,omitempty"`
	RepositoryURL     string    `json:"repository_url"`
	RecoveryIssueURL  string    `json:"recovery_issue_url"`
	RemoteHeadSHA     string    `json:"remote_head_sha,omitempty"`
	SyncedAt          string    `json:"synced_at"`
}

type pullRequest struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
	Merged  bool   `json:"merged"`
	Head    struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
}

type checkRunsResponse struct {
	CheckRuns []checkRun `json:"check_runs"`
}

type checkRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	HeadSHA    string `json:"head_sha"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Completed  string `json:"completed_at"`
	App        struct {
		ID int64 `json:"id"`
	} `json:"app"`
	Output struct {
		Summary string `json:"summary"`
		Text    string `json:"text"`
	} `json:"output"`
}

func Sync(ctx context.Context, repository passportrepo.Repository, controllerAppID int64, runner Runner) (Result, error) {
	if controllerAppID <= 0 {
		return Result{}, errors.New("controller App ID is not configured in this launcher release")
	}
	repositoryURL := "https://github.com/" + repository.Owner + "/" + repository.Name
	assessmentRepositoryURL := "https://github.com/" + repository.UpstreamOwner + "/" + repository.UpstreamName
	recoveryURL := assessmentRepositoryURL + "/issues/new?template=passport-help.yml"
	syncedAt := time.Now().UTC().Format(time.RFC3339)
	pullsPath := fmt.Sprintf(
		"repos/%s/%s/pulls?state=all&head=%s&per_page=100",
		url.PathEscape(repository.UpstreamOwner),
		url.PathEscape(repository.UpstreamName),
		url.QueryEscape(repository.Owner+":"+repository.Branch),
	)
	rawPulls, err := runner.Run(ctx, repository.Root, "api", pullsPath)
	if err != nil {
		return Result{}, fmt.Errorf("query assessment pull request: %w", err)
	}
	var pulls []pullRequest
	if err := json.Unmarshal(rawPulls, &pulls); err != nil {
		return Result{}, fmt.Errorf("parse assessment pull request: %w", err)
	}
	openMatching := make([]pullRequest, 0, 1)
	closedMatching := make([]pullRequest, 0, 1)
	for _, pull := range pulls {
		if pull.Merged || pull.Head.Ref != repository.Branch {
			continue
		}
		if pull.State == "open" {
			openMatching = append(openMatching, pull)
		} else if pull.State == "closed" {
			closedMatching = append(closedMatching, pull)
		}
	}
	if len(openMatching) > 1 {
		return Result{}, fmt.Errorf("expected at most one open assessment pull request, found %d", len(openMatching))
	}
	var pull pullRequest
	if len(openMatching) == 1 {
		pull = openMatching[0]
	} else if len(closedMatching) > 0 {
		sort.Slice(closedMatching, func(i, j int) bool { return closedMatching[i].Number > closedMatching[j].Number })
		pull = closedMatching[0]
	} else {
		return Result{
			State:            StateRecoveryRequired,
			RepositoryURL:    repositoryURL,
			RecoveryIssueURL: recoveryURL,
			SyncedAt:         syncedAt,
		}, nil
	}
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(pull.Head.SHA) {
		return Result{}, errors.New("assessment pull request has an invalid remote head SHA")
	}
	checksPath := fmt.Sprintf(
		"repos/%s/%s/commits/%s/check-runs?per_page=100",
		url.PathEscape(repository.UpstreamOwner),
		url.PathEscape(repository.UpstreamName),
		pull.Head.SHA,
	)
	rawChecks, err := runner.Run(ctx, repository.Root, "api", checksPath)
	if err != nil {
		return Result{}, fmt.Errorf("query controller Check Run: %w", err)
	}
	var checks checkRunsResponse
	if err := json.Unmarshal(rawChecks, &checks); err != nil {
		return Result{}, fmt.Errorf("parse controller Check Run: %w", err)
	}
	candidates := make([]checkRun, 0, 1)
	for _, check := range checks.CheckRuns {
		if check.Name == checkName && check.App.ID == controllerAppID && check.HeadSHA == pull.Head.SHA && check.Status == "completed" {
			candidates = append(candidates, check)
		}
	}
	if len(candidates) == 0 {
		for _, check := range checks.CheckRuns {
			if check.Name == checkName && check.HeadSHA == pull.Head.SHA && check.App.ID != controllerAppID {
				return Result{}, errors.New("a same-named Check Run exists for the current head but not from the trusted controller App")
			}
		}
		return Result{
			State:             StateWaitingForController,
			PullRequestNumber: pull.Number,
			PullRequestURL:    pull.HTMLURL,
			RepositoryURL:     repositoryURL,
			RecoveryIssueURL:  recoveryURL,
			RemoteHeadSHA:     pull.Head.SHA,
			SyncedAt:          syncedAt,
		}, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Completed < candidates[j].Completed
	})
	check := candidates[len(candidates)-1]
	status, err := parseMarker(check.Output.Summary + "\n" + check.Output.Text)
	if err != nil {
		return Result{}, err
	}
	if status.HeadSHA != pull.Head.SHA {
		return Result{}, errors.New("controller marker SHA differs from the trusted Check Run SHA")
	}
	if status.CurriculumVersion != repository.Passport.CurriculumVersion {
		return Result{}, errors.New("controller marker curriculum differs from this passport")
	}
	if pull.State == "closed" && status.Stage != "complete" {
		return Result{
			State:             StateRecoveryRequired,
			PullRequestNumber: pull.Number,
			PullRequestURL:    pull.HTMLURL,
			RepositoryURL:     repositoryURL,
			RecoveryIssueURL:  recoveryURL,
			RemoteHeadSHA:     pull.Head.SHA,
			SyncedAt:          syncedAt,
		}, nil
	}
	if err := validateRoute(status, repository.Passport.Missions); err != nil {
		return Result{}, err
	}
	official := Official{
		Source:            "github_controller_check",
		ControllerAppID:   controllerAppID,
		CheckRunID:        check.ID,
		CheckRunURL:       fmt.Sprintf("https://github.com/%s/%s/runs/%d", repository.UpstreamOwner, repository.UpstreamName, check.ID),
		CheckConclusion:   check.Conclusion,
		PullRequestNumber: pull.Number,
		PullRequestURL:    pull.HTMLURL,
		RepositoryURL:     repositoryURL,
		RecoveryIssueURL:  recoveryURL,
		RemoteHeadSHA:     pull.Head.SHA,
		Status:            status,
		SyncedAt:          syncedAt,
	}
	return Result{
		State:             StateVerified,
		Official:          &official,
		PullRequestNumber: pull.Number,
		PullRequestURL:    pull.HTMLURL,
		RepositoryURL:     repositoryURL,
		RecoveryIssueURL:  recoveryURL,
		RemoteHeadSHA:     pull.Head.SHA,
		SyncedAt:          syncedAt,
	}, nil
}

func parseMarker(value string) (ControllerStatus, error) {
	matches := markerPattern.FindAllStringSubmatch(value, -1)
	if len(matches) != 1 {
		return ControllerStatus{}, errors.New("trusted Check Run must contain exactly one controller status marker")
	}
	if len(matches[0][1]) < 16 || len(matches[0][1]) > 20000 {
		return ControllerStatus{}, errors.New("controller status marker has an invalid size")
	}
	raw, err := base64.RawURLEncoding.DecodeString(matches[0][1])
	if err != nil {
		return ControllerStatus{}, errors.New("controller status marker is invalid base64url")
	}
	var status ControllerStatus
	if err := strictJSON(raw, &status); err != nil {
		return ControllerStatus{}, fmt.Errorf("parse controller status marker: %w", err)
	}
	if status.SchemaVersion != 2 || status.CurriculumVersion == "" || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(status.HeadSHA) {
		return ControllerStatus{}, errors.New("controller status marker has unsupported identity fields")
	}
	if status.Stage != "working" && status.Stage != "blocked" && status.Stage != "awaiting_operational_approval" && status.Stage != "complete" {
		return ControllerStatus{}, errors.New("controller status marker has an invalid stage")
	}
	if status.ReviewState != "not_ready" && status.ReviewState != "requested" && status.ReviewState != "changes_requested" && status.ReviewState != "approved" {
		return ControllerStatus{}, errors.New("controller status marker has an invalid review state")
	}
	if _, err := time.Parse(time.RFC3339, status.AssessedAt); err != nil {
		return ControllerStatus{}, errors.New("controller status marker has an invalid assessment time")
	}
	return status, nil
}

func validateRoute(status ControllerStatus, assigned []string) error {
	if len(status.Missions) != len(assigned) {
		return errors.New("controller status route length differs from the assigned passport")
	}
	currentFound := status.CurrentMission == nil
	for index, mission := range status.Missions {
		if mission.ID != assigned[index] {
			return errors.New("controller status route order differs from the assigned passport")
		}
		if mission.Status != "locked" && mission.Status != "needs_work" && mission.Status != "awaiting_operational_approval" && mission.Status != "passed" {
			return errors.New("controller status contains an invalid mission state")
		}
		if status.CurrentMission != nil && mission.ID == *status.CurrentMission {
			currentFound = true
		}
	}
	if !currentFound {
		return errors.New("controller status current mission is outside the assigned route")
	}
	if status.Stage == "complete" && status.CurrentMission != nil {
		return errors.New("complete controller status still identifies a current mission")
	}
	if status.Stage != "complete" && status.CurrentMission == nil {
		return errors.New("incomplete controller status has no current mission")
	}
	return nil
}

func strictJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON value")
	}
	return nil
}
