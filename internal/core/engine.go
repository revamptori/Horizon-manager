// Package core implements Horizon's local server manager without a UI or network control surface.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const Version = "0.2.0-go-preview.1"
const manifestURL = "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json"
const maxDownload int64 = 200_000_000

type Options struct {
	DataDir  string
	Platform string
}
type Engine struct {
	dataDir       string
	platform      string
	client        *http.Client
	manifest      string
	allowTestHTTP bool // Never enabled by a production constructor.
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.Mutex
	startMu       sync.Mutex
	processes     map[string]*managedProcess
	directories   map[string]*sync.Mutex
	closing       bool
	preferencesMu sync.Mutex
	events        *eventBus
	OnShutdown    func()
}

func New(options Options) (*Engine, error) {
	if !filepath.IsAbs(options.DataDir) {
		return nil, errors.New("application data directory must be absolute")
	}
	if err := os.MkdirAll(options.DataDir, 0700); err != nil {
		return nil, err
	}
	dir, err := filepath.EvalSymlinks(options.DataDir)
	if err != nil {
		return nil, err
	}
	platform := options.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 30 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{dataDir: dir, platform: platform, ctx: ctx, cancel: cancel,
		manifest: manifestURL, processes: map[string]*managedProcess{}, directories: map[string]*sync.Mutex{}, events: &eventBus{}}
	e.client = &http.Client{Transport: transport, Timeout: 10 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many download redirects")
			}
			return e.validateDownloadURL(req.URL.String())
		}}
	return e, nil
}

// Invoke is the shared desktop/JNI command boundary. No shell commands are accepted.
func (e *Engine) Invoke(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	e.mu.Lock()
	closing := e.closing
	e.mu.Unlock()
	if closing {
		return nil, errors.New("the local host is shutting down")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(e.ctx, cancel)
	defer stop()
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	switch method {
	case "host_capabilities":
		return map[string]any{"version": Version, "platform": e.platform, "hostingAvailable": e.platform != "android", "dataDirectory": e.dataDir}, nil
	case "network_snapshot":
		return networkSnapshot()
	case "network_changed":
		e.events.publish("network-changed", map[string]string{"message": "Network changed; refresh the selected private interface before starting a server."})
		return nil, nil
	case "check_port_available":
		var p struct {
			Address string `json:"address"`
			Port    int    `json:"port"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return checkPort(p.Address, p.Port)
	case "default_server_directory":
		var p struct {
			Name string `json:"name"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return e.defaultDirectory(p.Name)
	case "prepare_server_directory":
		var p struct {
			Directory string `json:"directory"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return e.prepareDirectory(p.Directory)
	case "list_minecraft_versions":
		return e.listVersions(ctx)
	case "install_vanilla_server":
		var p InstallRequest
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return e.install(ctx, p)
	case "set_server_binding":
		var p struct {
			ServerID  string `json:"serverId"`
			Directory string `json:"directory"`
			Address   string `json:"address"`
			Port      int    `json:"port"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return nil, e.setBinding(p.ServerID, p.Directory, p.Address, p.Port)
	case "start_server":
		var p StartRequest
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return e.start(p)
	case "server_status", "stop_server":
		var p struct {
			ServerID string `json:"serverId"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if !safeID(p.ServerID) {
			return nil, errors.New("invalid local server identifier")
		}
		if method == "server_status" {
			return e.status(p.ServerID), nil
		}
		return e.stop(ctx, p.ServerID)
	case "send_server_command":
		var p struct {
			ServerID string `json:"serverId"`
			Command  string `json:"command"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return nil, e.sendCommand(p.ServerID, p.Command)
	case "create_backup":
		var p struct {
			ServerID  string `json:"serverId"`
			Directory string `json:"directory"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return e.backup(ctx, p.ServerID, p.Directory)
	case "restore_backup":
		var p RestoreRequest
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return e.restore(ctx, p)
	case "read_preferences":
		return e.readPreferences()
	case "write_preferences":
		var p struct {
			Values map[string]string `json:"values"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return nil, e.writePreferences(p.Values)
	case "poll_events":
		var p struct {
			After uint64 `json:"after"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		return e.events.poll(p.After), nil
	case "shutdown_host":
		if e.OnShutdown == nil {
			return nil, errors.New("close the Android app normally; there is no background HTTP host")
		}
		go func() { time.Sleep(300 * time.Millisecond); e.OnShutdown() }()
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported host command: %s", method)
	}
}

func decode(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid command parameters: %w", err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("unexpected trailing parameters")
	}
	return nil
}

// Close cancels downloads/file operations and stops Java gracefully before a bounded forced shutdown.
func (e *Engine) Close(ctx context.Context) error {
	e.mu.Lock()
	e.closing = true
	ids := make([]string, 0, len(e.processes))
	for id := range e.processes {
		ids = append(ids, id)
	}
	e.mu.Unlock()
	e.cancel()
	var wg sync.WaitGroup
	errs := make(chan error, len(ids))
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := e.stop(ctx, id)
			if err != nil {
				errs <- err
			}
		}(id)
	}
	wg.Wait()
	close(errs)
	var all []error
	for err := range errs {
		all = append(all, err)
	}
	if tr, ok := e.client.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
	return errors.Join(all...)
}

func (e *Engine) lockDirectory(dir string) func() {
	key := dir
	if e.platform == "windows" {
		key = strings.ToLower(key)
	}
	e.mu.Lock()
	mutex := e.directories[key]
	if mutex == nil {
		mutex = &sync.Mutex{}
		e.directories[key] = mutex
	}
	e.mu.Unlock()
	mutex.Lock()
	return mutex.Unlock
}
