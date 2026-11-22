// Package desktop serves the embedded UI on authenticated, host-checked loopback only.
package desktop

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
)

type Caller interface {
	Invoke(context.Context, string, json.RawMessage) (any, error)
}
type Handler struct {
	origin, host, token string
	assets              fs.FS
	caller              Caller
}

func NewHandler(origin, token string, assets fs.FS, caller Caller) (http.Handler, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "" || u.User != nil {
		return nil, errors.New("desktop host must use an explicit IPv4 loopback origin")
	}
	if len(token) < 32 {
		return nil, errors.New("session token is too short")
	}
	return &Handler{origin: origin, host: u.Host, token: token, assets: assets, caller: caller}, nil
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(remote) == nil || !net.ParseIP(remote).IsLoopback() {
		http.Error(w, "loopback access required", http.StatusForbidden)
		return
	}
	if r.Host != h.host {
		http.Error(w, "invalid host", http.StatusForbidden)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != h.origin {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "cross-site access denied", http.StatusForbidden)
		return
	}
	if r.URL.Path == "/api/call" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Horizon-Token")), []byte(h.token)) != 1 {
			http.Error(w, "session authorization required; reopen the app from its launcher", http.StatusUnauthorized)
			return
		}
		if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
			http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := decoder.Decode(&request); err != nil || len(request.Method) > 64 || request.Method == "" {
			http.Error(w, "invalid command request", http.StatusBadRequest)
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			http.Error(w, "unexpected trailing JSON", http.StatusBadRequest)
			return
		}
		result, err := h.caller.Invoke(r.Context(), request.Method, request.Params)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"result": result})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/host.js" {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		io.WriteString(w, "window.__HORIZON_HOST__ = 'desktop';\n")
		return
	}
	if r.URL.Path != "/" {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}
		if _, err := fs.Stat(h.assets, name); err != nil {
			http.NotFound(w, r)
			return
		}
	}
	http.FileServer(http.FS(h.assets)).ServeHTTP(w, r)
}
