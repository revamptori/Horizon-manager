package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type ServerStatus struct {
	Running  bool `json:"running"`
	PID      *int `json:"pid"`
	ExitCode *int `json:"exitCode"`
}
type StartRequest struct {
	ServerID  string  `json:"serverId"`
	Directory string  `json:"directory"`
	MemoryMB  int     `json:"memoryMb"`
	JavaPath  *string `json:"javaPath"`
}
type managedProcess struct {
	command   *exec.Cmd
	input     io.WriteCloser
	mu        sync.Mutex
	stopMu    sync.Mutex
	writeMu   sync.Mutex
	done      chan struct{}
	running   bool
	exitCode  *int
	directory string
}

func (p *managedProcess) status() ServerStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running {
		return ServerStatus{ExitCode: p.exitCode}
	}
	pid := p.command.Process.Pid
	return ServerStatus{Running: true, PID: &pid}
}
func (e *Engine) status(id string) ServerStatus {
	e.mu.Lock()
	p := e.processes[id]
	e.mu.Unlock()
	if p == nil {
		return ServerStatus{}
	}
	return p.status()
}
func (e *Engine) ensureStopped(id, dir string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for key, p := range e.processes {
		if (key == id || p.directory == dir) && p.status().Running {
			return errors.New("stop this server before changing its files or creating a manual backup")
		}
	}
	return nil
}
func (e *Engine) start(p StartRequest) (ServerStatus, error) {
	e.startMu.Lock()
	defer e.startMu.Unlock()
	if e.platform == "android" {
		return ServerStatus{}, errors.New("Minecraft hosting is unavailable on Android: this preview does not include a compatible Android JVM; desktop Java cannot run here")
	}
	if !safeID(p.ServerID) {
		return ServerStatus{}, errors.New("invalid local server identifier")
	}
	if p.MemoryMB < 512 || p.MemoryMB > 32768 {
		return ServerStatus{}, errors.New("memory allocation must be between 512 and 32768 MB")
	}
	dir, err := e.directory(p.Directory, false)
	if err != nil {
		return ServerStatus{}, err
	}
	unlock := e.lockDirectory(dir)
	defer unlock()
	if err = e.ensureStopped(p.ServerID, dir); err != nil {
		return ServerStatus{}, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return ServerStatus{}, err
	}
	defer root.Close()
	if err = verifyInstalled(root, p.ServerID); err != nil {
		return ServerStatus{}, err
	}
	bytes, err := readRegular(root, "server.properties", 2<<20)
	if err != nil {
		return ServerStatus{}, err
	}
	address, port, err := parseBinding(bytes)
	if err != nil {
		return ServerStatus{}, err
	}
	if err = validateBinding(address, port); err != nil {
		return ServerStatus{}, err
	}
	eula, err := readRegular(root, "eula.txt", 8192)
	if err != nil {
		return ServerStatus{}, err
	}
	accepted := false
	for _, line := range strings.Split(string(eula), "\n") {
		key, value := property(line)
		if key == "eula" {
			accepted = value == "true"
		}
	}
	if !accepted {
		return ServerStatus{}, errors.New("the Minecraft EULA has not been accepted")
	}
	executable := "java"
	if p.JavaPath != nil && strings.TrimSpace(*p.JavaPath) != "" {
		executable = *p.JavaPath
	}
	java, err := exec.LookPath(executable)
	if err != nil {
		return ServerStatus{}, fmt.Errorf("Java was not found; install a compatible desktop JVM and add it to PATH: %w", err)
	}
	command := exec.Command(java, fmt.Sprintf("-Xms%dM", min(1024, p.MemoryMB)), fmt.Sprintf("-Xmx%dM", p.MemoryMB), "-jar", "server.jar", "nogui")
	command.WaitDelay = 2 * time.Second
	command.Dir = dir
	configureProcess(command)
	input, err := command.StdinPipe()
	if err != nil {
		return ServerStatus{}, err
	}
	out := &outputWriter{engine: e, id: p.ServerID, stream: "stdout"}
	stderr := &outputWriter{engine: e, id: p.ServerID, stream: "stderr"}
	command.Stdout = out
	command.Stderr = stderr
	if err = command.Start(); err != nil {
		input.Close()
		return ServerStatus{}, fmt.Errorf("could not launch Java: %w", err)
	}
	cleanup, err := containProcess(command)
	if err != nil {
		command.Process.Kill()
		command.Wait()
		input.Close()
		return ServerStatus{}, fmt.Errorf("could not contain the Java process safely: %w", err)
	}
	process := &managedProcess{command: command, input: input, done: make(chan struct{}), running: true, directory: dir}
	e.mu.Lock()
	if e.closing {
		e.mu.Unlock()
		command.Process.Kill()
		command.Wait()
		cleanup()
		input.Close()
		return ServerStatus{}, errors.New("host is shutting down")
	}
	e.processes[p.ServerID] = process
	e.mu.Unlock()
	initial := process.status()
	go func() {
		command.Wait()
		out.flush()
		stderr.flush()
		input.Close()
		cleanup()
		code := command.ProcessState.ExitCode()
		process.mu.Lock()
		process.running = false
		process.exitCode = &code
		process.mu.Unlock()
		close(process.done)
		e.events.publish("server-exited", map[string]any{"serverId": p.ServerID, "exitCode": code})
	}()
	return initial, nil
}
func (e *Engine) sendCommand(id, command string) error {
	if !safeID(id) {
		return errors.New("invalid local server identifier")
	}
	if len(command) > 2048 || strings.ContainsAny(command, "\r\n\x00") || strings.TrimSpace(command) == "" {
		return errors.New("enter a single non-empty server command under 2048 bytes")
	}
	e.mu.Lock()
	p := e.processes[id]
	e.mu.Unlock()
	if p == nil || !p.status().Running {
		return errors.New("commands can only be sent to a running server")
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err := io.WriteString(p.input, strings.TrimSpace(command)+"\n")
	return err
}
func (e *Engine) stop(ctx context.Context, id string) (ServerStatus, error) {
	e.mu.Lock()
	p := e.processes[id]
	e.mu.Unlock()
	if p == nil {
		return ServerStatus{}, nil
	}
	p.stopMu.Lock()
	defer p.stopMu.Unlock()
	if !p.status().Running {
		return p.status(), nil
	}
	// Write on a separate goroutine so a blocked JVM command pipe cannot prevent shutdown.
	go func() { p.writeMu.Lock(); defer p.writeMu.Unlock(); io.WriteString(p.input, "stop\n") }()
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case <-p.done:
		return p.status(), nil
	case <-ctx.Done():
	case <-timer.C:
	}
	if err := p.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return p.status(), err
	}
	<-p.done
	return p.status(), nil
}

type outputWriter struct {
	engine     *Engine
	id, stream string
	mu         sync.Mutex
	pending    string
}

func (w *outputWriter) emit(line string) {
	w.engine.events.publish("server-output", map[string]string{"serverId": w.id, "stream": w.stream, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "line": strings.TrimSuffix(line, "\r")})
}
func (w *outputWriter) Write(bytes []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending += string(bytes)
	for {
		index := strings.IndexByte(w.pending, '\n')
		if index < 0 {
			if len(w.pending) > 65536 {
				w.emit(w.pending[:65536] + " [line truncated]")
				w.pending = ""
			}
			break
		}
		line := w.pending[:index]
		if len(line) > 65536 {
			line = line[:65536] + " [line truncated]"
		}
		w.emit(line)
		w.pending = w.pending[index+1:]
	}
	return len(bytes), nil
}
func (w *outputWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending != "" {
		w.emit(w.pending)
		w.pending = ""
	}
}
