package starter

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/deployment"
)

// Resume never creates a fork, route or PR, nor publishes local changes.
func existingLearningRecord(ctx context.Context, runner commandRunner, username string) (string, error) {
	if err := verifyDirectFork(ctx, runner, username); err != nil {
		return "", errors.New("cannot find the existing Passport fork; check your network and use the same GitHub account as on the other computer; nothing was created")
	}
	path := "repos/" + sourceRepository + "/pulls?state=all&head=" + url.QueryEscape(username+":onboarding/"+strings.ToLower(username)) + "&base=main&per_page=100"
	raw, err := runner.Run(ctx, "", "gh", "api", path)
	if err != nil {
		return "", errors.New("cannot read the existing learning record; check the connection and retry; nothing was created")
	}
	var pulls []struct {
		State    string  `json:"state"`
		URL      string  `json:"html_url"`
		MergedAt *string `json:"merged_at"`
	}
	if json.Unmarshal(raw, &pulls) != nil {
		return "", errors.New("invalid learning-record response; nothing was created")
	}
	open := []string{}
	closed := ""
	for _, pull := range pulls {
		if pull.MergedAt != nil || !strings.HasPrefix(pull.URL, deployment.PullURLPrefix) {
			continue
		}
		if pull.State == "open" {
			open = append(open, pull.URL)
		}
		if pull.State == "closed" && closed == "" {
			closed = pull.URL
		}
	}
	if len(open) == 1 {
		return open[0], nil
	}
	if len(open) == 0 && closed != "" {
		return closed, nil
	}
	return "", errors.New("no single existing learning record was found; confirm the GitHub account or request help; nothing was created")
}

func cloneExistingPassport(ctx context.Context, runner commandRunner, username, directory string) error {
	if info, err := os.Lstat(directory); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("existing Passport location is not a regular directory; nothing was replaced")
		}
		gitInfo, err := os.Lstat(filepath.Join(directory, ".git"))
		if err != nil || gitInfo.Mode()&os.ModeSymlink != 0 {
			return errors.New("existing Passport folder is not a Git clone; nothing was replaced")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(directory), 0o755); err != nil {
		return err
	}
	_, err := runner.Run(ctx, "", "git", "clone", "https://github.com/"+username+"/"+deployment.ExerciseName+".git", directory)
	return err
}
