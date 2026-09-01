package localserver

import (
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
	"github.com/soheylm-passport-sandbox/gh-passport/internal/localstate"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/passportrepo"
	"github.com/soheylm-passport-sandbox/gh-passport/internal/webdist"
)

const cookieName = "ideal_passport_local_session"

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
	official        *githubstatus.Official
	syncResult      *githubstatus.Result
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
	return target, false, nil
}

func (server *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /__passport/start/{token}", server.startSession)
	mux.HandleFunc("GET /__passport/v1/health", server.health)
	mux.HandleFunc("GET /__passport/v1/context", server.withSession(server.context))
	mux.HandleFunc("PUT /__passport/v1/state", server.withSession(server.updateState))
	mux.HandleFunc("POST /__passport/v1/sync", server.withSession(server.sync))
	mux.Handle("/", server.staticHandler())
	return server.securityHeaders(server.hostGuard(mux))
}

func (server *Server) Close(ctx context.Context) error {
	if server.http == nil {
		return nil
	}
	err := server.http.Shutdown(ctx)
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
	http.Redirect(response, request, "/passport/", http.StatusSeeOther)
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
	server.mu.Lock()
	payload.Official = server.official
	payload.SyncResult = server.syncResult
	server.mu.Unlock()
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
	if state.LastOfficialSync != existing.LastOfficialSync || state.LastSeenHeadSHA != existing.LastSeenHeadSHA {
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
			"detail": "Run gh passport doctor, then open the permanent assessment PR if the problem continues.",
		})
		return
	}
	if result.Official != nil {
		if err := server.store.SaveStatus(result.Official); err != nil {
			server.writeError(response, http.StatusInternalServerError, "status_cache_write_failed")
			return
		}
	}
	server.syncResult = &result
	server.official = result.Official
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
