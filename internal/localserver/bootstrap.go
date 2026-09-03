package localserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/soheylm-passport-sandbox/gh-passport/internal/webdist"
)

// BootstrapSelection is the only browser input accepted before provisioning.
type BootstrapSelection struct {
	Platform            string   `json:"platform"`
	Responsibilities    []string `json:"responsibilities"`
	PublicRecordConsent bool     `json:"public_record_consent"`
}

// BootstrapProvision creates the managed transport after explicit consent and
// returns the one-time URL of the fully provisioned local passport.
type BootstrapProvision func(context.Context, BootstrapSelection) (string, error)

// BootstrapServer serves the pre-provisioning wizard without a Git checkout.
type BootstrapServer struct {
	assets    fs.FS
	provision BootstrapProvision
	token     string
	cspNonce  string
	origin    string
	listener  net.Listener
	http      *http.Server
	mu        sync.Mutex
	started   bool
}

func NewBootstrap(provision BootstrapProvision) (*BootstrapServer, error) {
	if provision == nil {
		return nil, errors.New("bootstrap provision callback is required")
	}
	assets, err := fs.Sub(webdist.Assets, "bundle")
	if err != nil {
		return nil, err
	}
	random := make([]byte, 64)
	if _, err := rand.Read(random); err != nil {
		return nil, fmt.Errorf("generate bootstrap session: %w", err)
	}
	return &BootstrapServer{
		assets: assets, provision: provision,
		token: hex.EncodeToString(random[:32]), cspNonce: hex.EncodeToString(random[32:]),
	}, nil
}

func (server *BootstrapServer) Start() (string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("listen on loopback: %w", err)
	}
	server.listener = listener
	server.origin = "http://" + listener.Addr().String()
	server.http = &http.Server{
		Handler: server.handler(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 20 * time.Second, WriteTimeout: 10 * time.Minute,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	go func() { _ = server.http.Serve(listener) }()
	return server.origin + "/__passport/bootstrap/" + server.token, nil
}

func (server *BootstrapServer) Close(ctx context.Context) error {
	if server.http == nil {
		return nil
	}
	return server.http.Shutdown(ctx)
}

func (server *BootstrapServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /__passport/bootstrap/{token}", server.startSession)
	mux.HandleFunc("GET /__passport/v1/context", server.withSession(server.context))
	mux.HandleFunc("POST /__passport/v2/setup", server.withSession(server.setup))
	mux.Handle("/", server.staticHandler())
	return server.securityHeaders(server.hostGuard(mux))
}

func (server *BootstrapServer) startSession(response http.ResponseWriter, request *http.Request) {
	if request.PathValue("token") != server.token {
		http.NotFound(response, request)
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name: cookieName, Value: server.token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: 60 * 60,
	})
	http.Redirect(response, request, "/start/", http.StatusSeeOther)
}

func (server *BootstrapServer) context(response http.ResponseWriter, _ *http.Request) {
	platform := runtime.GOOS
	if platform == "darwin" {
		platform = "macos"
	}
	server.writeJSON(response, http.StatusOK, map[string]any{
		"mode": "bootstrap", "detected_platform": platform,
	})
}

func (server *BootstrapServer) setup(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, 16<<10)
	decoder := json.NewDecoder(io.LimitReader(request.Body, (16<<10)+1))
	decoder.DisallowUnknownFields()
	var selection BootstrapSelection
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
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.started {
		server.writeError(response, http.StatusConflict, "bootstrap_already_completed")
		return
	}
	target, err := server.provision(request.Context(), selection)
	if err != nil {
		server.writeJSON(response, http.StatusConflict, map[string]string{
			"error": "provisioning_stopped_safely", "detail": err.Error(),
		})
		return
	}
	server.started = true
	server.writeJSON(response, http.StatusCreated, map[string]string{"status": "ready", "next": target})
}

func (server *BootstrapServer) staticHandler() http.Handler {
	files := http.FileServer(http.FS(server.assets))
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
		files.ServeHTTP(response, request)
	})
}

func (server *BootstrapServer) withSession(next http.HandlerFunc) http.HandlerFunc {
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

func (server *BootstrapServer) hostGuard(next http.Handler) http.Handler {
	expected := strings.TrimPrefix(server.origin, "http://")
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if expected == "" || request.Host != expected {
			http.Error(response, "invalid loopback host", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (server *BootstrapServer) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self' 'nonce-"+server.cspNonce+"'; connect-src 'self' http://127.0.0.1:*; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(response, request)
	})
}

func (server *BootstrapServer) writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func (server *BootstrapServer) writeError(response http.ResponseWriter, status int, code string) {
	server.writeJSON(response, status, map[string]string{"error": code})
}
