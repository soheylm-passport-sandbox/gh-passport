package localserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/githubstatus"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/launcherupdate"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/localstate"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/missionverify"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/passportrepo"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/webdist"
)

const cookieName = "ideal_passport_local_session"

type LauncherUpdater interface {
	Check(context.Context) launcherupdate.Status
	Prepare(context.Context) (launcherupdate.Prepared, error)
}

type Server struct {
	repository      passportrepo.Repository
	store           localstate.Store
	controllerAppID int64
	github          githubstatus.Runner
	runner          passportrepo.CommandRunner
	assets          fs.FS
	token           string
	cspNonce        string
	listener        net.Listener
	http            *http.Server
	origin          string
	lockPath        string
	mu              sync.Mutex
	statusMu        sync.RWMutex
	official        *githubstatus.Official
	syncResult      *githubstatus.Result
	updateQueued    bool
	updater         LauncherUpdater
	updateReady     chan<- launcherupdate.Prepared
}

type ContextPayload struct {
	Mode          string                  `json:"mode"`
	Repository    passportrepo.Repository `json:"repository"`
	LocalState    localstate.State        `json:"local_state"`
	Official      *githubstatus.Official  `json:"official,omitempty"`
	SyncResult    *githubstatus.Result    `json:"sync_result,omitempty"`
	OfficialError string                  `json:"official_error,omitempty"`
	StateSource   string                  `json:"state_source"`
}

func New(
	repository passportrepo.Repository,
	controllerAppID int64,
	github githubstatus.Runner,
	runner passportrepo.CommandRunner,
) (*Server, error) {
	if !passportrepo.IsLocalStateIgnored(repository, runner) {
		return nil, errors.New(".passport-local is not gitignored; refusing to create local state")
	}
	assets, err := fs.Sub(webdist.Assets, "bundle")
	if err != nil {
		return nil, err
	}
	tokenBytes := make([]byte, 64)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("generate local session: %w", err)
	}
	return &Server{
		repository:      repository,
		store:           localstate.New(repository.Root),
		controllerAppID: controllerAppID,
		github:          github,
		runner:          runner,
		assets:          assets,
		token:           hex.EncodeToString(tokenBytes[:32]),
		cspNonce:        hex.EncodeToString(tokenBytes[32:]),
	}, nil
}

// EnableUpdates connects the loopback UI to the trusted launcher updater.
// It is optional so static previews and unit fixtures cannot initiate updates.
func (server *Server) EnableUpdates(
	updater LauncherUpdater,
	ready chan<- launcherupdate.Prepared,
) {
	server.updater = updater
	server.updateReady = ready
}

func (server *Server) Start() (string, bool, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", false, fmt.Errorf("listen on loopback: %w", err)
	}
	server.listener = listener
	server.origin = "http://" + listener.Addr().String()
	server.http = &http.Server{
		Handler:           server.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	go func() {
		_ = server.http.Serve(listener)
	}()
	target := server.origin + "/__passport/start/" + server.token
	existing, err := server.claimInstance(target)
	if err != nil {
		_ = server.http.Close()
		return "", false, err
	}
	if existing != "" {
		_ = server.http.Close()
		return existing, true, nil
	}
	if err := server.recordLaunch(); err != nil {
		_ = server.http.Close()
		server.releaseInstance()
		return "", false, err
	}
	return target, false, nil
}

func (server *Server) recordLaunch() error {
	state, err := server.store.Load()
	if err != nil {
		return fmt.Errorf("read local resume state: %w", err)
	}
	if state.SchemaVersion == 0 {
		state = server.defaultState()
	}
	state.LaunchCount++
	if err := server.store.Save(state); err != nil {
		return fmt.Errorf("record local passport launch: %w", err)
	}
	return nil
}

func (server *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /__passport/start/{token}", server.startSession)
	mux.HandleFunc("GET /__passport/v1/health", server.health)
	mux.HandleFunc("GET /__passport/v1/context", server.withSession(server.context))
	mux.HandleFunc("GET /__passport/v1/update", server.withSession(server.updateStatus))
	mux.HandleFunc("POST /__passport/v1/update", server.withSession(server.startUpdate))
	mux.HandleFunc("PUT /__passport/v1/state", server.withSession(server.updateState))
	mux.HandleFunc("POST /__passport/v1/sync", server.withSession(server.sync))
	mux.HandleFunc("POST /__passport/v2/verify", server.withSession(server.verifyMission))
	mux.HandleFunc("POST /__passport/v2/submit", server.withSession(server.submitMission))
	mux.HandleFunc("POST /__passport/v2/setup", server.withSession(server.completeSetup))
	mux.HandleFunc("POST /__passport/v2/practice", server.withSession(server.preparePractice))
	mux.Handle("/", server.staticHandler())
	return server.securityHeaders(server.hostGuard(mux))
}

func (server *Server) updateStatus(response http.ResponseWriter, request *http.Request) {
	if server.updater == nil {
		http.NotFound(response, request)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()
	server.writeJSON(response, http.StatusOK, server.updater.Check(ctx))
}

func (server *Server) startUpdate(response http.ResponseWriter, request *http.Request) {
	if server.updater == nil || server.updateReady == nil {
		http.NotFound(response, request)
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.updateQueued {
		server.writeError(response, http.StatusConflict, "launcher_update_already_starting")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	prepared, err := server.updater.Prepare(ctx)
	if err != nil {
		server.writeJSON(response, http.StatusServiceUnavailable, map[string]string{
			"error":  "launcher_update_unavailable",
			"detail": "The update could not be verified. Keep using the current Passport and try again later.",
		})
		return
	}
	server.updateQueued = true
	server.writeJSON(response, http.StatusAccepted, map[string]string{
		"status":  "update_starting",
		"version": prepared.Version,
		"message": "The local Passport will close, update, and reopen in a new browser tab.",
	})
	go func() {
		time.Sleep(250 * time.Millisecond)
		server.updateReady <- prepared
	}()
}

func (server *Server) Close(ctx context.Context) error {
	if server.http == nil {
		return nil
	}
	err := server.http.Shutdown(ctx)
	if err != nil {
		_ = server.http.Close()
	}
	server.releaseInstance()
	return err
}

type instanceRecord struct {
	SchemaVersion int    `json:"schema_version"`
	PID           int    `json:"pid"`
	Origin        string `json:"origin"`
	StartURL      string `json:"start_url"`
	StartedAt     string `json:"started_at"`
}

func (server *Server) claimInstance(target string) (string, error) {
	directory := filepath.Dir(server.store.Path())
	if err := ensureLocalDirectory(directory); err != nil {
		return "", err
	}
	server.lockPath = filepath.Join(directory, "server.json")
	for attempt := 0; attempt < 3; attempt++ {
		file, err := os.OpenFile(server.lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			record := instanceRecord{1, os.Getpid(), server.origin, target, time.Now().UTC().Format(time.RFC3339)}
			encoder := json.NewEncoder(file)
			writeErr := encoder.Encode(record)
			if syncErr := file.Sync(); writeErr == nil {
				writeErr = syncErr
			}
			if closeErr := file.Close(); writeErr == nil {
				writeErr = closeErr
			}
			if writeErr != nil {
				_ = os.Remove(server.lockPath)
				return "", fmt.Errorf("write local server lock: %w", writeErr)
			}
			return "", nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("create local server lock: %w", err)
		}
		record, readErr := readInstance(server.lockPath)
		if readErr == nil && instanceHealthy(record) {
			return record.StartURL, nil
		}
		if attempt == 0 {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if removeErr := os.Remove(server.lockPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return "", fmt.Errorf("remove stale local server lock: %w", removeErr)
		}
	}
	return "", errors.New("could not claim or reuse the local passport server")
}

func (server *Server) releaseInstance() {
	if server.lockPath == "" {
		return
	}
	record, err := readInstance(server.lockPath)
	if err == nil && record.PID == os.Getpid() && record.Origin == server.origin {
		_ = os.Remove(server.lockPath)
	}
}

func readInstance(path string) (instanceRecord, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return instanceRecord{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 4096 {
		return instanceRecord{}, errors.New("local server lock is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return instanceRecord{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 4097))
	decoder.DisallowUnknownFields()
	var record instanceRecord
	if err := decoder.Decode(&record); err != nil {
		return instanceRecord{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return instanceRecord{}, errors.New("local server lock contains trailing JSON")
	}
	if record.SchemaVersion != 1 || record.PID <= 0 || !validStartURL(record.Origin, record.StartURL) {
		return instanceRecord{}, errors.New("local server lock has invalid fields")
	}
	return record, nil
}

func validStartURL(origin, target string) bool {
	parsedOrigin, err := url.Parse(origin)
	if err != nil || parsedOrigin.Scheme != "http" || parsedOrigin.Hostname() != "127.0.0.1" || parsedOrigin.Port() == "" {
		return false
	}
	parsedTarget, err := url.Parse(target)
	return err == nil && parsedTarget.Scheme == "http" && parsedTarget.Host == parsedOrigin.Host &&
		regexp.MustCompile(`^/__passport/start/[a-f0-9]{64}$`).MatchString(parsedTarget.Path)
}

func instanceHealthy(record instanceRecord) bool {
	transport := &http.Transport{Proxy: nil}
	client := &http.Client{Transport: transport, Timeout: 750 * time.Millisecond}
	response, err := client.Get(record.Origin + "/__passport/v1/health")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}

func ensureLocalDirectory(directory string) error {
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

func OpenBrowser(target string) error {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" {
		return errors.New("refusing to open a non-loopback passport URL")
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		command = exec.Command("open", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("open default browser: %w", err)
	}
	return nil
}

func (server *Server) startSession(response http.ResponseWriter, request *http.Request) {
	if request.PathValue("token") != server.token {
		http.NotFound(response, request)
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name:     cookieName,
		Value:    server.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   8 * 60 * 60,
	})
	target := "/passport/"
	if !server.repository.Passport.SetupComplete {
		target = "/start/"
	}
	http.Redirect(response, request, target, http.StatusSeeOther)
}

func (server *Server) health(response http.ResponseWriter, _ *http.Request) {
	server.writeJSON(response, http.StatusOK, map[string]string{"status": "local-only"})
}

func (server *Server) context(response http.ResponseWriter, _ *http.Request) {
	state, err := server.store.Load()
	if err != nil {
		_, backupErr := server.store.BackupCorrupt()
		if backupErr != nil {
			server.writeError(response, http.StatusConflict, "local_state_corrupt")
			return
		}
		state = server.defaultState()
		if err := server.store.Save(state); err != nil {
			server.writeError(response, http.StatusInternalServerError, "local_state_write_failed")
			return
		}
		payload := ContextPayload{
			Mode:          "local",
			Repository:    server.safeRepository(),
			LocalState:    state,
			OfficialError: "Corrupt local navigation state was backed up locally and rebuilt.",
			StateSource:   "local_navigation_only",
		}
		server.writeJSON(response, http.StatusOK, payload)
		return
	}
	if state.SchemaVersion == 0 {
		state = server.defaultState()
		if err := server.store.Save(state); err != nil {
			server.writeError(response, http.StatusInternalServerError, "local_state_write_failed")
			return
		}
	}
	payload := ContextPayload{
		Mode:        "local",
		Repository:  server.safeRepository(),
		LocalState:  state,
		StateSource: "local_navigation_only",
	}
	server.statusMu.RLock()
	payload.Official = server.official
	payload.SyncResult = server.syncResult
	server.statusMu.RUnlock()
	server.writeJSON(response, http.StatusOK, payload)
}

func (server *Server) updateState(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, 64<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var state localstate.State
	if err := decoder.Decode(&state); err != nil {
		server.writeError(response, http.StatusBadRequest, "invalid_local_state")
		return
	}
	if state.PassportID != server.defaultState().PassportID || state.CurriculumVersion != server.repository.Passport.CurriculumVersion || state.RouteDigest != server.defaultState().RouteDigest {
		server.writeError(response, http.StatusConflict, "local_state_identity_mismatch")
		return
	}
	missionAssigned := false
	for _, mission := range server.repository.Passport.Missions {
		if state.LastOpenedMission == mission {
			missionAssigned = true
			break
		}
	}
	if !missionAssigned {
		server.writeError(response, http.StatusBadRequest, "local_state_mission_outside_route")
		return
	}
	existing, err := server.store.Load()
	if err != nil {
		server.writeError(response, http.StatusConflict, "local_state_unavailable")
		return
	}
	if state.LastOfficialSync != existing.LastOfficialSync ||
		state.LastSeenHeadSHA != existing.LastSeenHeadSHA ||
		state.LaunchCount != existing.LaunchCount {
		server.writeError(response, http.StatusForbidden, "official_sync_fields_are_read_only")
		return
	}
	if err := server.store.Save(state); err != nil {
		server.writeError(response, http.StatusBadRequest, "invalid_local_state")
		return
	}
	server.writeJSON(response, http.StatusOK, map[string]string{"status": "saved_locally_not_completion"})
}

func (server *Server) sync(response http.ResponseWriter, request *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	repository, err := passportrepo.Find(server.repository.Root, server.runner)
	if err != nil {
		server.writeError(response, http.StatusConflict, "repository_state_unavailable")
		return
	}
	result, err := githubstatus.Sync(ctx, repository, server.controllerAppID, server.github)
	if err != nil {
		server.writeJSON(response, http.StatusServiceUnavailable, map[string]string{
			"error":  "official_status_unavailable",
			"detail": "Run gh passport doctor, then use the dashboard recovery link if the problem continues.",
		})
		return
	}
	if result.Official != nil {
		if err := server.store.SaveStatus(result.Official); err != nil {
			server.writeError(response, http.StatusInternalServerError, "status_cache_write_failed")
			return
		}
	}
	server.statusMu.Lock()
	server.syncResult = &result
	server.official = result.Official
	server.statusMu.Unlock()
	state, _ := server.store.Load()
	state.LastOfficialSync = result.SyncedAt
	state.LastSeenHeadSHA = result.RemoteHeadSHA
	if state.SchemaVersion == 0 {
		state = server.defaultState()
		state.LastOfficialSync = result.SyncedAt
		state.LastSeenHeadSHA = result.RemoteHeadSHA
	}
	_ = server.store.Save(state)
	server.writeJSON(response, http.StatusOK, result)
}

func (server *Server) decodeAttempt(response http.ResponseWriter, request *http.Request) (missionverify.Catalog, missionverify.Attempt, bool) {
	request.Body = http.MaxBytesReader(response, request.Body, 64<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var attempt missionverify.Attempt
	if err := decoder.Decode(&attempt); err != nil {
		server.writeError(response, http.StatusBadRequest, "invalid_mission_attempt")
		return missionverify.Catalog{}, missionverify.Attempt{}, false
	}
	assigned := false
	for _, mission := range server.repository.Passport.Missions {
		if attempt.Mission == mission {
			assigned = true
			break
		}
	}
	if !assigned {
		server.writeError(response, http.StatusForbidden, "mission_outside_assigned_route")
		return missionverify.Catalog{}, missionverify.Attempt{}, false
	}
	if attempt.Mission != server.activeMissionID() {
		server.writeError(response, http.StatusConflict, "mission_not_current")
		return missionverify.Catalog{}, missionverify.Attempt{}, false
	}
	catalog, err := missionverify.LoadCatalog(server.repository.Root)
	if err != nil || catalog.CurriculumVersion != server.repository.Passport.CurriculumVersion {
		server.writeError(response, http.StatusConflict, "mission_catalog_unavailable")
		return missionverify.Catalog{}, missionverify.Attempt{}, false
	}
	return catalog, attempt, true
}

func (server *Server) activeMissionID() string {
	server.statusMu.RLock()
	defer server.statusMu.RUnlock()
	if server.official != nil {
		if server.official.Status.CurrentMission == nil {
			return ""
		}
		return *server.official.Status.CurrentMission
	}
	if len(server.repository.Passport.Missions) == 0 {
		return ""
	}
	return server.repository.Passport.Missions[0]
}

func (server *Server) verifyMission(response http.ResponseWriter, request *http.Request) {
	catalog, attempt, ok := server.decodeAttempt(response, request)
	if !ok {
		return
	}
	mission, result, err := missionverify.Grade(catalog, attempt)
	if err != nil {
		server.writeError(response, http.StatusBadRequest, "mission_verification_failed")
		return
	}
	receipt, err := server.localReceipt(mission, attempt.LocalInput)
	if err != nil {
		server.writeError(response, http.StatusConflict, "local_verifier_failed_safely")
		return
	}
	result.Receipt = receipt
	if receipt["passed"] != true {
		result.Status = "needs_work"
	}
	state, _ := server.store.Load()
	if state.AttemptCounts == nil {
		state.AttemptCounts = map[string]int{}
	}
	state.AttemptCounts[attempt.Mission]++
	result.NextVariant = state.AttemptCounts[attempt.Mission]
	_ = server.store.Save(state)
	server.writeJSON(response, http.StatusOK, result)
}

func (server *Server) submitMission(response http.ResponseWriter, request *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	catalog, attempt, ok := server.decodeAttempt(response, request)
	if !ok {
		return
	}
	mission, result, err := missionverify.Grade(catalog, attempt)
	if err != nil {
		server.writeError(response, http.StatusBadRequest, "mission_verification_failed")
		return
	}
	receipt, err := server.localReceipt(mission, attempt.LocalInput)
	if err != nil || result.Status != "ready_to_submit" || receipt["passed"] != true {
		server.writeJSON(response, http.StatusUnprocessableEntity, map[string]any{
			"error": "mission_not_ready", "result": result,
		})
		return
	}
	if !attempt.Attestation.Reviewed || !attempt.Attestation.NoSecrets || !attempt.Attestation.ObservedResult {
		server.writeError(response, http.StatusUnprocessableEntity, "attestation_required")
		return
	}
	if err := server.commitSubmission(catalog, attempt, mission, receipt); err != nil {
		server.writeJSON(response, http.StatusConflict, map[string]string{
			"error": "submission_not_published", "detail": err.Error(),
		})
		return
	}
	server.writeJSON(response, http.StatusCreated, map[string]any{
		"status": "queued", "mission": attempt.Mission,
		"message": "Submitted once. Wait for the trusted controller result.",
	})
}

type setupRequest struct {
	Platform            string   `json:"platform"`
	Responsibilities    []string `json:"responsibilities"`
	PublicRecordConsent bool     `json:"public_record_consent"`
}

func (server *Server) completeSetup(response http.ResponseWriter, request *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.repository.Passport.SetupComplete {
		server.writeError(response, http.StatusConflict, "passport_setup_already_complete")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 16<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var selection setupRequest
	if err := decoder.Decode(&selection); err != nil {
		server.writeError(response, http.StatusBadRequest, "invalid_route_selection")
		return
	}
	if selection.Platform != "windows" && selection.Platform != "macos" && selection.Platform != "linux" {
		server.writeError(response, http.StatusBadRequest, "unsupported_platform")
		return
	}
	if !selection.PublicRecordConsent {
		server.writeError(response, http.StatusUnprocessableEntity, "public_record_consent_required")
		return
	}
	catalog, err := missionverify.LoadCatalog(server.repository.Root)
	if err != nil {
		server.writeError(response, http.StatusConflict, "mission_catalog_unavailable")
		return
	}
	missions, err := missionverify.Resolve(catalog, selection.Responsibilities)
	if err != nil {
		server.writeError(response, http.StatusBadRequest, "invalid_route_selection")
		return
	}
	value := server.repository.Passport
	value.Platform = selection.Platform
	value.Responsibilities = append([]string(nil), selection.Responsibilities...)
	value.Missions = missions
	value.SetupComplete = true
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		server.writeError(response, http.StatusInternalServerError, "route_generation_failed")
		return
	}
	encoded = append(encoded, '\n')
	path := filepath.Join(server.repository.Root, "passport.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		server.writeError(response, http.StatusInternalServerError, "route_write_failed")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Minute)
	defer cancel()
	if _, err := server.runner.Run(ctx, server.repository.Root, "git", "add", "--", "passport.json"); err != nil {
		server.writeJSON(response, http.StatusConflict, map[string]string{"error": "route_publish_failed", "detail": "The route file was kept locally. Press Create my Passport once more when the connection is available."})
		return
	}
	staged, err := server.runner.Run(ctx, server.repository.Root, "git", "diff", "--cached", "--name-only")
	if err != nil || !onlyExpectedPaths(string(staged), map[string]bool{"passport.json": true}) {
		server.writeJSON(response, http.StatusConflict, map[string]string{"error": "route_publish_failed", "detail": "The route file was kept locally, but the managed staging area was not as expected. Run gh passport doctor."})
		return
	}
	if strings.TrimSpace(string(staged)) != "" {
		if _, err := server.runner.Run(ctx, server.repository.Root, "git", "diff", "--cached", "--check"); err != nil {
			server.writeJSON(response, http.StatusConflict, map[string]string{"error": "route_publish_failed", "detail": "The generated route did not pass its safety check. Run gh passport doctor."})
			return
		}
		if _, err := server.runner.Run(ctx, server.repository.Root, "git", "commit", "-m", "chore(passport): configure learning route"); err != nil {
			server.writeJSON(response, http.StatusConflict, map[string]string{"error": "route_publish_failed", "detail": "The route file was kept locally. Press Create my Passport once more to resume."})
			return
		}
	}
	if _, err := server.runner.Run(ctx, server.repository.Root, "git", "push", "origin", server.repository.Branch); err != nil {
		server.writeJSON(response, http.StatusConflict, map[string]string{"error": "route_publish_failed", "detail": "The route is committed locally. Press Create my Passport once more when the connection is available; do not start over."})
		return
	}
	server.repository.Passport = value
	server.repository, _ = passportrepo.Find(server.repository.Root, server.runner)
	if err := server.store.Save(server.defaultState()); err != nil {
		server.writeError(response, http.StatusInternalServerError, "local_state_write_failed")
		return
	}
	server.writeJSON(response, http.StatusCreated, map[string]any{"status": "ready", "missions": missions, "next": "/passport/"})
}

func (server *Server) practiceRoot() string {
	if filepath.Base(server.repository.Root) == ".transport" {
		return filepath.Join(filepath.Dir(server.repository.Root), "practice")
	}
	return filepath.Join(server.repository.Root, "practice")
}

func samePath(left, right string) bool {
	leftAbsolute, leftErr := filepath.Abs(left)
	rightAbsolute, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	leftAbsolute = filepath.Clean(leftAbsolute)
	rightAbsolute = filepath.Clean(rightAbsolute)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(leftAbsolute, rightAbsolute)
	}
	return leftAbsolute == rightAbsolute
}

func (server *Server) preparePractice(response http.ResponseWriter, _ *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := server.practiceRoot()
	origin := "https://github.com/" + server.repository.Owner + "/" + server.repository.Name + ".git"
	if info, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		if _, err := server.runner.Run(ctx, "", "git", "clone", origin, root); err != nil {
			server.writeError(response, http.StatusConflict, "practice_clone_failed")
			return
		}
	} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		server.writeError(response, http.StatusConflict, "practice_path_not_safe")
		return
	}
	remote, err := server.runner.Run(ctx, root, "git", "remote", "get-url", "origin")
	if err != nil || !strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(string(remote)), ".git"), strings.TrimSuffix(origin, ".git")) {
		server.writeError(response, http.StatusConflict, "practice_origin_mismatch")
		return
	}
	if _, err := server.runner.Run(ctx, root, "git", "fetch", "--prune", "origin"); err != nil {
		server.writeError(response, http.StatusConflict, "practice_fetch_failed")
		return
	}
	branch := "practice/" + strings.ToLower(server.repository.Passport.GitHubUser)
	if _, err := server.runner.Run(ctx, root, "git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		_, err = server.runner.Run(ctx, root, "git", "switch", branch)
	} else if _, remoteErr := server.runner.Run(ctx, root, "git", "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch); remoteErr == nil {
		_, err = server.runner.Run(ctx, root, "git", "switch", "--create", branch, "--track", "origin/"+branch)
	} else {
		_, err = server.runner.Run(ctx, root, "git", "switch", "--create", branch, "origin/main")
	}
	if err != nil {
		server.writeError(response, http.StatusConflict, "practice_branch_failed")
		return
	}
	server.writeJSON(response, http.StatusOK, map[string]string{"status": "ready", "path": root, "branch": branch})
}

func (server *Server) verifyGitEnvironment() map[string]bool {
	root := server.practiceRoot()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	activeUser, activeUserErr := server.runner.Run(ctx, root, "gh", "api", "user", "--jq", ".login")
	checks := map[string]bool{
		"git": server.fixedCommandOK(root, 10*time.Second, "git", "--version"),
		"github_auth": activeUserErr == nil && strings.EqualFold(
			strings.TrimSpace(string(activeUser)),
			server.repository.Passport.GitHubUser,
		),
	}
	name, nameErr := server.runner.Run(ctx, root, "git", "config", "--get", "user.name")
	email, emailErr := server.runner.Run(ctx, root, "git", "config", "--get", "user.email")
	remotes, remotesErr := server.runner.Run(ctx, root, "git", "remote", "-v")
	branch, branchErr := server.runner.Run(ctx, root, "git", "branch", "--show-current")
	top, topErr := server.runner.Run(ctx, root, "git", "rev-parse", "--show-toplevel")
	identityName := strings.TrimSpace(string(name))
	identityEmail := strings.TrimSpace(string(email))
	expectedBranch := "practice/" + strings.ToLower(server.repository.Passport.GitHubUser)
	checks["identity_name"] = nameErr == nil && identityName != "" && !passportrepo.IsManagedIdentity(identityName, identityEmail)
	checks["identity_email"] = emailErr == nil && strings.Contains(identityEmail, "@") && !passportrepo.IsManagedIdentity(identityName, identityEmail)
	checks["remote_credentials_absent"] = remotesErr == nil && remotesContainNoCredentials(string(remotes))
	checks["practice_repository"] = topErr == nil && samePath(strings.TrimSpace(string(top)), root)
	checks["practice_branch"] = branchErr == nil && strings.TrimSpace(string(branch)) == expectedBranch
	return checks
}

func (server *Server) verifyAIConfiguration() map[string]bool {
	root := server.practiceRoot()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	top, topErr := server.runner.Run(ctx, root, "git", "rev-parse", "--show-toplevel")
	branch, branchErr := server.runner.Run(ctx, root, "git", "branch", "--show-current")
	remotes, remotesErr := server.runner.Run(ctx, root, "git", "remote", "-v")
	status, statusErr := server.runner.Run(
		ctx,
		root,
		"git",
		"status",
		"--porcelain=v1",
		"--untracked-files=all",
		"--",
		"workspace/agent_task",
		".vscode",
		".zed",
		".env",
		".env.local",
	)
	expectedBranch := "practice/" + strings.ToLower(server.repository.Passport.GitHubUser)
	return map[string]bool{
		"practice_repository":       topErr == nil && samePath(strings.TrimSpace(string(top)), root),
		"practice_branch":           branchErr == nil && strings.TrimSpace(string(branch)) == expectedBranch,
		"practice_paths_clean":      statusErr == nil && strings.TrimSpace(string(status)) == "",
		"remote_credentials_absent": remotesErr == nil && remotesContainNoCredentials(string(remotes)),
	}
}

func (server *Server) verifySSHConfig() map[string]bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := server.runner.Run(ctx, server.repository.Root, "ssh", "-G", "euler")
	values := map[string]string{}
	identities := []string{}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.ToLower(fields[0])
		if key == "identityfile" {
			identities = append(identities, strings.Join(fields[1:], " "))
			continue
		}
		if _, exists := values[key]; !exists {
			values[key] = strings.Join(fields[1:], " ")
		}
	}
	privateIdentitySelection := len(identities) > 0
	for _, rawIdentity := range identities {
		identity := strings.ToLower(filepath.ToSlash(strings.TrimSpace(rawIdentity)))
		if identity == "none" || strings.HasSuffix(identity, ".pub") {
			privateIdentitySelection = false
		}
	}
	user := values["user"]
	identitiesOnly := values["identitiesonly"]
	return map[string]bool{
		"hostname":                   err == nil && strings.EqualFold(values["hostname"], "euler.ethz.ch"),
		"direct_connection":          values["port"] == "22" && values["proxycommand"] == "" && values["proxyjump"] == "",
		"short_username":             regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(user),
		"private_identity_selection": privateIdentitySelection,
		"identity_policy_resolved":   strings.EqualFold(identitiesOnly, "yes") || strings.EqualFold(identitiesOnly, "no"),
		"publickey_only":             strings.EqualFold(values["preferredauthentications"], "publickey"),
		"password_disabled":          strings.EqualFold(values["passwordauthentication"], "no"),
		"interactive_disabled":       strings.EqualFold(values["kbdinteractiveauthentication"], "no"),
		"forward_agent_disabled":     strings.EqualFold(values["forwardagent"], "no"),
	}
}

func (server *Server) localReceipt(mission missionverify.Mission, input map[string]string) (map[string]any, error) {
	receipt, err := missionverify.ConfirmLive(mission, input)
	if err != nil || receipt["passed"] != true {
		return receipt, err
	}
	liveChecks := map[string]bool{}
	if mission.Verification.RequiresLiveConfirmation {
		var ok bool
		liveChecks, ok = receipt["checks"].(map[string]bool)
		if !ok || len(liveChecks) == 0 {
			return receipt, errors.New("live confirmation result is incomplete")
		}
	}
	verifier := mission.Verification.LocalVerifier
	checks := map[string]bool{}
	switch verifier {
	case "route_resume":
		state, err := server.store.Load()
		checks["resumed_session"] = err == nil && state.LaunchCount >= 2
	case "ssh_config":
		checks = server.verifySSHConfig()
	case "git_environment":
		checks = server.verifyGitEnvironment()
	case "git_practice":
		var practice map[string]any
		checks, practice = server.verifyPractice()
		passed := len(checks) > 0
		for _, value := range checks {
			passed = passed && value
		}
		return server.validateReceiptChecks(
			mission,
			map[string]any{"verifier": verifier, "passed": passed, "checks": checks, "practice": practice},
		)
	case "python_environment":
		checks = server.verifyPythonEnvironment()
	case "python_project":
		checks = server.verifyPythonProject()
	case "ai_configuration":
		checks = server.verifyAIConfiguration()
	case "ai_project":
		checks = server.verifyAgentProject()
	case "slurm_script":
		checks = server.verifySlurmArray()
	case "gpu_script":
		checks = server.verifyGPUScript()
	case "handover_document":
		checks = server.verifyHandover()
	default:
		return server.validateReceiptChecks(mission, receipt)
	}
	for name, value := range liveChecks {
		checks[name] = value
	}
	passed := len(checks) > 0
	for _, value := range checks {
		passed = passed && value
	}
	return server.validateReceiptChecks(
		mission,
		map[string]any{"verifier": verifier, "passed": passed, "checks": checks},
	)
}

func (server *Server) validateReceiptChecks(mission missionverify.Mission, receipt map[string]any) (map[string]any, error) {
	checks, ok := receipt["checks"].(map[string]bool)
	if !ok || len(checks) != len(mission.Verification.ReceiptChecks) {
		return receipt, errors.New("local verifier result differs from the curriculum contract")
	}
	for _, expected := range mission.Verification.ReceiptChecks {
		if _, exists := checks[expected]; !exists {
			return receipt, errors.New("local verifier result differs from the curriculum contract")
		}
	}
	return receipt, nil
}

func remotesContainNoCredentials(value string) bool {
	for _, line := range strings.Split(value, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		raw := fields[1]
		if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") {
			afterScheme := strings.SplitN(raw, "://", 2)[1]
			if authority := strings.SplitN(afterScheme, "/", 2)[0]; strings.Contains(authority, "@") {
				return false
			}
		}
	}
	return true
}

func (server *Server) fixedCommandOK(directory string, timeout time.Duration, name string, arguments ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, err := server.runner.Run(ctx, directory, name, arguments...)
	return err == nil
}

func (server *Server) verifyPractice() (map[string]bool, map[string]any) {
	root := server.practiceRoot()
	expectedBranch := "practice/" + strings.ToLower(server.repository.Passport.GitHubUser)
	branch, branchErr := server.runner.Run(context.Background(), root, "git", "branch", "--show-current")
	changed, changedErr := server.runner.Run(context.Background(), root, "git", "diff", "--name-only", "origin/main...HEAD")
	subjects, logErr := server.runner.Run(context.Background(), root, "git", "log", "--format=%s", "origin/main..HEAD")
	lines := strings.Fields(strings.TrimSpace(string(changed)))
	allowed := len(lines) == 1 && lines[0] == "workspace/manual_task/project-note.md"
	conventional := false
	for _, subject := range strings.Split(strings.TrimSpace(string(subjects)), "\n") {
		if regexp.MustCompile(`^(docs|feat|fix|test)\([a-z0-9._/-]+\): [^\s].+`).MatchString(subject) {
			conventional = true
		}
	}
	prQuery := fmt.Sprintf("is:pr is:open base:main head:%s", expectedBranch)
	prOutput, prErr := server.fixedCommandOutput(root, 20*time.Second, "gh", "pr", "list", "--repo", server.repository.Owner+"/"+server.repository.Name, "--search", prQuery, "--json", "number,headRefOid,isDraft", "--jq", `if length == 1 and .[0].isDraft == true then {number: .[0].number, head_sha: .[0].headRefOid} else null end`)
	var practice struct {
		Number  int    `json:"number"`
		HeadSHA string `json:"head_sha"`
	}
	prOK := prErr == nil && json.Unmarshal(prOutput, &practice) == nil && practice.Number > 0 && regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(practice.HeadSHA)
	checks := map[string]bool{
		"practice_branch":     branchErr == nil && strings.TrimSpace(string(branch)) == expectedBranch,
		"bounded_diff":        changedErr == nil && allowed,
		"conventional_commit": logErr == nil && conventional,
		"draft_pr":            prOK,
	}
	reference := map[string]any{
		"repository":   server.repository.Owner + "/" + server.repository.Name,
		"branch":       expectedBranch,
		"head_sha":     practice.HeadSHA,
		"pull_request": practice.Number,
	}
	return checks, reference
}

func (server *Server) fixedCommandOutput(directory string, timeout time.Duration, name string, arguments ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GH_PAGER=cat", "NO_COLOR=1")
	return command.Output()
}

func (server *Server) practicePython() (string, string) {
	root := server.practiceRoot()
	if runtime.GOOS == "windows" {
		return root, filepath.Join(root, ".venv", "python.exe")
	}
	return root, filepath.Join(root, ".venv", "bin", "python")
}

func (server *Server) verifyPythonEnvironment() map[string]bool {
	root, python := server.practicePython()
	environmentPath := filepath.Join(root, ".venv")
	environmentInfo, environmentErr := os.Lstat(environmentPath)
	realEnvironment := environmentErr == nil && environmentInfo.IsDir() && environmentInfo.Mode()&os.ModeSymlink == 0
	// Conda commonly installs bin/python as a relative symlink to python3.x.
	// Follow that final link, then verify the interpreter from inside the env.
	info, err := os.Stat(python)
	condaHistory, condaErr := os.Lstat(filepath.Join(root, ".venv", "conda-meta", "history"))
	definitionPath := filepath.Join(root, "environment.yml")
	definitionInfo, definitionErr := os.Lstat(definitionPath)
	definition, definitionReadErr := os.ReadFile(definitionPath)
	definitionTracked := server.fixedCommandOK(root, 5*time.Second, "git", "ls-files", "--error-unmatch", "environment.yml")
	definitionUnchanged := server.fixedCommandOK(root, 5*time.Second, "git", "diff", "--quiet", "HEAD", "--", "environment.yml")
	ignored := server.fixedCommandOK(root, 5*time.Second, "git", "check-ignore", "--quiet", ".venv/conda-meta/history")
	interpreter := realEnvironment && err == nil && info.Mode().IsRegular() && server.fixedCommandOK(
		root,
		10*time.Second,
		python,
		"-I",
		"-c",
		"import pathlib, sys; assert pathlib.Path(sys.prefix).resolve() == pathlib.Path(sys.argv[1]).resolve(); assert sys.version_info[:2] == (3, 11)",
		environmentPath,
	)
	return map[string]bool{
		"venv_exists":       realEnvironment && err == nil && info.Mode().IsRegular(),
		"venv_ignored":      ignored,
		"venv_interpreter":  interpreter,
		"conda_environment": condaErr == nil && condaHistory.Mode().IsRegular(),
		"environment_definition": definitionErr == nil && definitionInfo.Mode().IsRegular() &&
			definitionInfo.Mode()&os.ModeSymlink == 0 && definitionReadErr == nil && len(definition) < 64<<10 &&
			bytes.Contains(definition, []byte("conda-forge")) && bytes.Contains(definition, []byte("nodefaults")) &&
			bytes.Contains(definition, []byte("python=3.11")) &&
			definitionTracked && definitionUnchanged,
	}
}

func (server *Server) verifyPythonProject() map[string]bool {
	root, python := server.practicePython()
	project := filepath.Join(root, "workspace", "python_project")
	source, sourceErr := os.ReadFile(filepath.Join(project, "passport_example.py"))
	tests, testsErr := os.ReadFile(filepath.Join(project, "tests", "test_passport_example.py"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	changed, changedErr := server.runner.Run(
		ctx,
		root,
		"git",
		"diff",
		"--name-only",
		"HEAD",
		"--",
		"workspace/python_project",
	)
	changedPaths := strings.Fields(strings.TrimSpace(string(changed)))
	allowedChanges := len(changedPaths) == 2 && onlyExpectedPaths(
		string(changed),
		map[string]bool{
			"workspace/python_project/passport_example.py":            true,
			"workspace/python_project/tests/test_passport_example.py": true,
		},
	)
	sourceText := string(source)
	testText := string(tests)
	return map[string]bool{
		"visible_tests": server.fixedCommandOK(project, 20*time.Second, python, "-m", "unittest", "discover", "-s", "tests", "-v"),
		"regression_test": testsErr == nil && len(tests) < 100_000 && strings.Contains(
			testText,
			"self.assertEqual(total_memory_gib(4, 3), 12)",
		),
		"correct_behavior": sourceErr == nil && len(source) < 100_000 && regexp.MustCompile(
			`(?m)^\s*return cpus \* memory_per_cpu_gib\s*$`,
		).MatchString(sourceText),
		"input_validation": sourceErr == nil && strings.Contains(sourceText, "if cpus < 1 or memory_per_cpu_gib < 1:") && strings.Contains(sourceText, "raise ValueError"),
		"bounded_diff":     changedErr == nil && allowedChanges,
	}
}

func (server *Server) verifyAgentProject() map[string]bool {
	root := server.practiceRoot()
	path := filepath.Join(root, "workspace", "agent_task", "storage-plan.md")
	content, err := os.ReadFile(path)
	text := strings.ToLower(string(content))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	changed, changedErr := server.runner.Run(
		ctx, root, "git", "diff", "--name-only", "HEAD", "--", "workspace/agent_task",
	)
	changedPaths := strings.Fields(strings.TrimSpace(string(changed)))
	canary, canaryErr := os.ReadFile(filepath.Join(root, "workspace", "agent_task", "scope-canary.txt"))
	return map[string]bool{
		"bounded_file":     err == nil && len(content) < 100_000,
		"bounded_diff":     changedErr == nil && len(changedPaths) == 1 && changedPaths[0] == "workspace/agent_task/storage-plan.md",
		"canary_unchanged": canaryErr == nil && string(canary) == "IDEAL-PASSPORT-AGENT-SCOPE-CANARY-v1\n",
		"durable_p":        strings.Contains(text, "p:") && strings.Contains(text, "durable"),
		"temporary_d":      strings.Contains(text, "d:") && strings.Contains(text, "temporary"),
		"avoid_c":          strings.Contains(text, "c:") && strings.Contains(text, "not"),
		"heavy_compute":    strings.Contains(text, "euler") || strings.Contains(text, "approved compute"),
	}
}

func slurmDirectives(text string) map[string]string {
	directives := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#SBATCH ") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "#SBATCH "))
		if key, value, found := strings.Cut(raw, "="); found {
			directives[strings.TrimSpace(key)] = strings.TrimSpace(value)
			continue
		}
		parts := strings.Fields(raw)
		if len(parts) == 1 {
			directives[parts[0]] = ""
		} else if len(parts) > 1 {
			directives[parts[0]] = strings.Join(parts[1:], " ")
		}
	}
	return directives
}

func (server *Server) readPracticeFixture(relative string) ([]byte, map[string]string, bool) {
	content, err := os.ReadFile(filepath.Join(server.practiceRoot(), filepath.FromSlash(relative)))
	return content, slurmDirectives(string(content)), err == nil && len(content) < 100_000
}

func logTemplateHas(value string, placeholders ...string) bool {
	if value == "" {
		return false
	}
	for _, placeholder := range placeholders {
		if !strings.Contains(value, placeholder) {
			return false
		}
	}
	return true
}

func (server *Server) verifySlurmArray() map[string]bool {
	_, directives, bounded := server.readPracticeFixture("workspace/slurm/array_job.slurm.txt")
	return map[string]bool{
		"bounded_file":   bounded,
		"account":        directives["--account"] == "es_fuge",
		"array_exact":    directives["--array"] == "0-9%1",
		"output_unique":  logTemplateHas(directives["--output"], "%A", "%a"),
		"error_unique":   logTemplateHas(directives["--error"], "%A", "%a"),
		"safe_resources": directives["--time"] == "00:10:00" && directives["--cpus-per-task"] == "1" && strings.EqualFold(directives["--mem-per-cpu"], "1G"),
	}
}

func slurmSeconds(value string) (int, bool) {
	match := regexp.MustCompile(`^(?:(\d+)-)?(\d{1,2}):(\d{2}):(\d{2})$`).FindStringSubmatch(value)
	if match == nil {
		return 0, false
	}
	numbers := make([]int, 4)
	for index, raw := range match[1:] {
		if raw == "" {
			continue
		}
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return 0, false
		}
		numbers[index] = parsed
	}
	if numbers[2] >= 60 || numbers[3] >= 60 {
		return 0, false
	}
	return numbers[0]*86400 + numbers[1]*3600 + numbers[2]*60 + numbers[3], true
}

func (server *Server) verifyGPUScript() map[string]bool {
	_, directives, bounded := server.readPracticeFixture("workspace/slurm/gpu_job.slurm.txt")
	gpuOK := regexp.MustCompile(`^(?:rtx_4090|rtx_3090|pro_6000):1$`).MatchString(directives["--gpus"])
	cpus, cpuErr := strconv.Atoi(directives["--cpus-per-task"])
	memoryMatch := regexp.MustCompile(`(?i)^([1-9]\d*)G$`).FindStringSubmatch(directives["--mem-per-cpu"])
	memoryOK := false
	if cpuErr == nil && 1 <= cpus && cpus <= 16 && memoryMatch != nil {
		memory, err := strconv.Atoi(memoryMatch[1])
		memoryOK = err == nil && cpus*memory <= 64
	}
	seconds, timeOK := slurmSeconds(directives["--time"])
	_, hasMem := directives["--mem"]
	return map[string]bool{
		"bounded_file": bounded,
		"account":      directives["--account"] == "es_fuge",
		"gpu_model":    gpuOK,
		"no_partition": func() bool { _, exists := directives["--partition"]; return !exists }(),
		"cpu_limit":    cpuErr == nil && 1 <= cpus && cpus <= 16,
		"memory_limit": memoryOK && !hasMem,
		"time_limit":   timeOK && seconds > 0 && seconds <= 4*3600,
		"output_log":   logTemplateHas(directives["--output"], "%x", "%j"),
		"error_log":    logTemplateHas(directives["--error"], "%x", "%j"),
	}
}

func (server *Server) verifyHandover() map[string]bool {
	content, err := os.ReadFile(filepath.Join(server.practiceRoot(), "workspace", "handover", "project-handover.md"))
	text := string(content)
	has := func(pattern string) bool { return regexp.MustCompile(pattern).MatchString(text) }
	dateMatch := regexp.MustCompile(`(?m)^Temporary-copy deletion date:\s*(20\d{2}-[01]\d-[0-3]\d)\s*$`).FindStringSubmatch(text)
	futureDate := false
	if dateMatch != nil {
		if deletionDate, parseErr := time.Parse("2006-01-02", dateMatch[1]); parseErr == nil {
			today := time.Now().UTC().Truncate(24 * time.Hour)
			futureDate = deletionDate.After(today)
		}
	}
	return map[string]bool{
		"owners":            err == nil && has(`(?m)^Current owner:\s*\S.+$`) && has(`(?m)^Authorized successor:\s*\S.+$`),
		"code_revision":     has(`(?m)^Revision:\s*[a-f0-9]{40}\s*$`),
		"data_locations":    has(`(?m)^Authoritative location:\s*\S.+$`) && has(`(?m)^Temporary locations to remove:\s*\S.+$`),
		"environment":       has(`(?m)^Environment definition:\s*\S.+$`),
		"reproduction":      has(`(?m)^Verification command:\s*\S.+$`) && has(`(?m)^Expected result:\s*\S.+$`),
		"retention":         has(`(?m)^Access owner:\s*\S.+$`) && has(`(?m)^Retention owner:\s*\S.+$`) && futureDate,
		"known_limitations": has(`(?m)^Unresolved risk or limitation:\s*\S.+$`),
		"no_placeholders":   err == nil && !strings.Contains(text, "REPLACE_ME") && !strings.Contains(text, "REPLACE_WITH_"),
	}
}

func (server *Server) commitSubmission(catalog missionverify.Catalog, attempt missionverify.Attempt, mission missionverify.Mission, receipt map[string]any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	repository, err := passportrepo.Find(server.repository.Root, server.runner)
	if err != nil {
		return err
	}
	relative := mission.Submission.Path
	stagedPaths := []string{relative}
	stagedPaths = append(stagedPaths, mission.Submission.ArtifactPaths...)
	allowed := make(map[string]bool, len(stagedPaths))
	for _, path := range stagedPaths {
		allowed[path] = true
	}
	if repository.Dirty {
		status, statusErr := server.runner.Run(ctx, repository.Root, "git", "status", "--porcelain=v1", "--untracked-files=normal")
		if statusErr != nil || !onlyManagedSubmissionChanges(string(status), allowed) {
			return errors.New("managed transport has unexpected local changes; no files were staged")
		}
	}
	target := filepath.Join(repository.Root, filepath.FromSlash(relative))
	cleanTarget := filepath.Clean(target)
	expectedRoot := filepath.Clean(filepath.Join(repository.Root, "submissions")) + string(os.PathSeparator)
	if !strings.HasPrefix(cleanTarget, expectedRoot) {
		return errors.New("submission path escaped its managed directory")
	}
	payload := map[string]any{
		"schema_version":     2,
		"curriculum_version": catalog.CurriculumVersion,
		"mission":            attempt.Mission,
		"answers":            attempt.Answers,
		"receipts":           receipt,
		"artifacts":          mission.Submission.ArtifactPaths,
		"attestation":        attempt.Attestation,
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > 64<<10 {
		return errors.New("generated submission exceeds the 64 KiB limit")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".passport-submission-*")
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
	if err := replaceRegularFile(temporaryName, target); err != nil {
		return err
	}
	if err := server.copyMissionArtifacts(repository.Root, mission.Submission.ArtifactPaths); err != nil {
		return err
	}
	arguments := append([]string{"add", "--"}, stagedPaths...)
	if _, err := server.runner.Run(ctx, repository.Root, "git", arguments...); err != nil {
		return fmt.Errorf("stage only the generated mission files: %w", err)
	}
	staged, err := server.runner.Run(ctx, repository.Root, "git", "diff", "--cached", "--name-only")
	if err != nil {
		return errors.New("cannot inspect generated mission files; commit was stopped")
	}
	if !onlyExpectedPaths(string(staged), allowed) {
		return errors.New("staged paths differ from the generated mission allowlist; commit was stopped")
	}
	changed := strings.Fields(strings.TrimSpace(string(staged)))
	if len(changed) == 0 {
		if repository.HeadSHA != repository.RemoteHeadSHA {
			if _, err := server.runner.Run(ctx, repository.Root, "git", "push", "origin", repository.Branch); err != nil {
				return fmt.Errorf("retry push of generated mission files: %w", err)
			}
		}
		server.repository, _ = passportrepo.Find(repository.Root, server.runner)
		return nil
	}
	if _, err := server.runner.Run(ctx, repository.Root, "git", "diff", "--cached", "--check"); err != nil {
		return errors.New("generated submission failed the staged diff check")
	}
	message := "chore(passport): submit " + attempt.Mission
	if _, err := server.runner.Run(ctx, repository.Root, "git", "commit", "-m", message); err != nil {
		return fmt.Errorf("commit generated submission: %w", err)
	}
	if _, err := server.runner.Run(ctx, repository.Root, "git", "push", "origin", repository.Branch); err != nil {
		return fmt.Errorf("push generated submission: %w", err)
	}
	server.repository, _ = passportrepo.Find(repository.Root, server.runner)
	return nil
}

func onlyExpectedPaths(output string, allowed map[string]bool) bool {
	for _, path := range strings.Fields(strings.TrimSpace(output)) {
		if !allowed[path] {
			return false
		}
	}
	return true
}

func onlyManagedSubmissionChanges(output string, allowed map[string]bool) bool {
	lines := strings.Split(strings.TrimRight(output, "\r\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return false
	}
	allowedStatus := map[string]bool{"??": true, " M": true, "M ": true, "A ": true, "AM": true, "MM": true}
	for _, line := range lines {
		if len(line) < 4 || line[2] != ' ' || !allowedStatus[line[:2]] || !allowed[line[3:]] {
			return false
		}
	}
	return true
}

func (server *Server) copyMissionArtifacts(transportRoot string, artifacts []string) error {
	practiceRoot := server.practiceRoot()
	for _, relative := range artifacts {
		if filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative))) != relative || !strings.HasPrefix(relative, "workspace/") {
			return errors.New("mission artifact path escaped the public workspace")
		}
		source, err := boundedRegularFile(practiceRoot, relative, 256<<10)
		if err != nil {
			return fmt.Errorf("practice artifact %s is not a bounded regular file: %w", relative, err)
		}
		content, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		target := filepath.Join(transportRoot, filepath.FromSlash(relative))
		if _, err := boundedPath(transportRoot, filepath.Dir(relative)); err != nil {
			return fmt.Errorf("transport artifact parent is unsafe: %w", err)
		}
		temporary, err := os.CreateTemp(filepath.Dir(target), ".passport-artifact-*")
		if err != nil {
			return err
		}
		name := temporary.Name()
		if err := temporary.Chmod(0o600); err == nil {
			_, err = temporary.Write(content)
		}
		closeErr := temporary.Close()
		if err != nil {
			os.Remove(name)
			return err
		}
		if closeErr != nil {
			os.Remove(name)
			return closeErr
		}
		if err := replaceRegularFile(name, target); err != nil {
			os.Remove(name)
			return err
		}
	}
	return nil
}

// replaceRegularFile keeps the previous managed file available for rollback.
// This also avoids relying on os.Rename overwriting an existing file on Windows.
func replaceRegularFile(temporary, target string) error {
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return os.Rename(temporary, target)
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed target is not a regular file")
	}

	backupFile, err := os.CreateTemp(filepath.Dir(target), ".passport-backup-*")
	if err != nil {
		return err
	}
	backup := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		os.Remove(backup)
		return err
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := os.Rename(target, backup); err != nil {
		return err
	}
	if err := os.Rename(temporary, target); err != nil {
		if rollbackErr := os.Rename(backup, target); rollbackErr != nil {
			return fmt.Errorf("replace managed file: %w; restore previous file: %v", err, rollbackErr)
		}
		return err
	}
	if err := os.Remove(backup); err != nil {
		return fmt.Errorf("remove managed file backup: %w", err)
	}
	return nil
}

func boundedRegularFile(root, relative string, maximum int64) (string, error) {
	path, err := boundedPath(root, relative)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximum {
		return "", errors.New("file type or size is not allowed")
	}
	return path, nil
}

func boundedPath(root, relative string) (string, error) {
	cleanRoot := filepath.Clean(root)
	cleanRelative := filepath.Clean(filepath.FromSlash(relative))
	if cleanRelative == "." || filepath.IsAbs(cleanRelative) || cleanRelative == ".." || strings.HasPrefix(cleanRelative, ".."+string(os.PathSeparator)) {
		return "", errors.New("path escapes its managed root")
	}
	current := cleanRoot
	parts := strings.Split(cleanRelative, string(os.PathSeparator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && index == len(parts)-1 {
			return current, nil
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("managed path contains a missing or symbolic-link component")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", errors.New("managed path parent is not a directory")
		}
	}
	return current, nil
}

func (server *Server) defaultState() localstate.State {
	route := strings.Join(server.repository.Passport.Missions, "\n")
	routeDigest := sha256.Sum256([]byte(route))
	passportID := sha256.Sum256([]byte(
		server.repository.Owner + "/" + server.repository.Name + ":" + server.repository.Passport.CurriculumVersion,
	))
	return localstate.State{
		SchemaVersion:     1,
		PassportID:        hex.EncodeToString(passportID[:12]),
		CurriculumVersion: server.repository.Passport.CurriculumVersion,
		RouteDigest:       hex.EncodeToString(routeDigest[:]),
		LastOpenedMission: server.repository.Passport.Missions[0],
		ExpandedHelp:      []string{},
		MissionDrafts:     map[string]map[string][]string{},
		AttemptCounts:     map[string]int{},
	}
}

func (server *Server) safeRepository() passportrepo.Repository {
	value := server.repository
	value.Root = ""
	return value
}

func (server *Server) staticHandler() http.Handler {
	fileServer := http.FileServer(http.FS(server.assets))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/__passport/") {
			http.NotFound(response, request)
			return
		}
		if candidate, ok := htmlAssetPath(request.URL.Path); ok {
			content, err := readBoundedAsset(server.assets, candidate, 4<<20)
			if err == nil {
				response.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = response.Write(injectScriptNonce(content, server.cspNonce))
				return
			}
		}
		fileServer.ServeHTTP(response, request)
	})
}

func htmlAssetPath(requestPath string) (string, bool) {
	candidate := strings.TrimPrefix(requestPath, "/")
	if candidate == "" || strings.HasSuffix(requestPath, "/") {
		candidate += "index.html"
	} else if !strings.HasSuffix(candidate, ".html") {
		return "", false
	}
	if !fs.ValidPath(candidate) {
		return "", false
	}
	return candidate, true
}

func readBoundedAsset(assets fs.FS, name string, limit int64) ([]byte, error) {
	file, err := assets.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("asset is not a bounded regular file")
	}
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(content)) > limit {
		return nil, errors.New("asset exceeds the configured size limit")
	}
	return content, nil
}

func injectScriptNonce(content []byte, nonce string) []byte {
	return []byte(strings.ReplaceAll(string(content), "<script", `<script nonce="`+nonce+`"`))
}

func (server *Server) withSession(next http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie(cookieName)
		if err != nil || cookie.Value != server.token {
			server.writeError(response, http.StatusUnauthorized, "local_session_required")
			return
		}
		if request.Method != http.MethodGet && request.Header.Get("Origin") != server.origin {
			server.writeError(response, http.StatusForbidden, "origin_mismatch")
			return
		}
		next(response, request)
	}
}

func (server *Server) hostGuard(next http.Handler) http.Handler {
	expected := strings.TrimPrefix(server.origin, "http://")
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if expected == "" || request.Host != expected {
			http.Error(response, "invalid loopback host", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (server *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self' 'nonce-"+server.cspNonce+"'; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(response, request)
	})
}

func (server *Server) writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func (server *Server) writeError(response http.ResponseWriter, status int, code string) {
	server.writeJSON(response, status, map[string]string{"error": code})
}

func ParseControllerAppID(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, errors.New("controller App ID must be a non-negative integer")
	}
	return parsed, nil
}
