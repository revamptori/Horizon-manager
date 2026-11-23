package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

type fakeCaller struct{ called int }

func (f *fakeCaller) Invoke(context.Context, string, json.RawMessage) (any, error) {
	f.called++
	return map[string]bool{"ok": true}, nil
}
func TestLoopbackAuthorizationAndOriginGuards(t *testing.T) {
	token := strings.Repeat("a", 64)
	caller := &fakeCaller{}
	handler, err := NewHandler("http://127.0.0.1:12345", token, fstest.MapFS{"index.html": {Data: []byte("Horizon")}}, caller)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, method, url, host, origin, remote, auth, body string
		status                                              int
	}{
		{name: "static", method: "GET", status: 200},
		{name: "no token", method: "POST", url: "/api/call", body: `{"method":"network_snapshot"}`, status: 401},
		{name: "query token rejected", method: "POST", url: "/api/call?token=" + token, status: 401},
		{name: "invalid token", method: "POST", url: "/api/call", auth: "wrong", status: 401},
		{name: "DNS rebinding", method: "POST", url: "/api/call", auth: token, host: "attacker.example:12345", status: 403},
		{name: "foreign origin", method: "POST", url: "/api/call", auth: token, origin: "https://attacker.example", status: 403},
		{name: "LAN client", method: "POST", url: "/api/call", auth: token, remote: "192.168.1.4:3000", status: 403},
		{name: "wrong method", method: "GET", url: "/api/call", auth: token, status: 405},
		{name: "malformed JSON", method: "POST", url: "/api/call", auth: token, body: `{`, status: 400},
		{name: "trailing JSON", method: "POST", url: "/api/call", auth: token, body: `{"method":"x"} {}`, status: 400},
		{name: "valid", method: "POST", url: "/api/call", auth: token, origin: "http://127.0.0.1:12345", body: `{"method":"network_snapshot","params":{}}`, status: 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.url
			if path == "" {
				path = "/"
			}
			req := httptest.NewRequest(tc.method, "http://127.0.0.1:12345"+path, strings.NewReader(tc.body))
			req.RemoteAddr = "127.0.0.1:3000"
			if tc.remote != "" {
				req.RemoteAddr = tc.remote
			}
			if tc.host != "" {
				req.Host = tc.host
			}
			req.Header.Set("X-Horizon-Token", tc.auth)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("want %d got %d: %s", tc.status, w.Code, w.Body.String())
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("CORS enabled")
			}
			if w.Header().Get("X-Frame-Options") != "DENY" {
				t.Fatal("framing allowed")
			}
			if strings.Contains(w.Body.String(), token) {
				t.Fatal("token leaked in response")
			}
		})
	}
	if caller.called != 1 {
		t.Fatal("unauthorized calls reached core", caller.called)
	}
	if _, err = NewHandler("http://0.0.0.0:12345", token, fstest.MapFS{}, caller); err == nil {
		t.Fatal("nonloopback control host accepted")
	}
}
func TestHostScriptDoesNotContainSessionToken(t *testing.T) {
	token := strings.Repeat("b", 64)
	handler, _ := NewHandler("http://127.0.0.1:12345", token, fstest.MapFS{}, &fakeCaller{})
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:12345/host.js", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "desktop") || strings.Contains(w.Body.String(), token) {
		t.Fatal(w.Body.String())
	}
}
