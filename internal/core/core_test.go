package core

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("HORIZON_TEST_JAVA") == "1" {
		fmt.Println("test JVM ready")
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			text := scanner.Text()
			if text == "stop" && os.Getenv("HORIZON_TEST_IGNORE_STOP") != "1" {
				os.Exit(0)
			}
			if text == "crash" {
				os.Exit(7)
			}
			fmt.Println("received: " + text)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func testEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		e.Close(ctx)
	})
	return e
}
func digest(bytes []byte) string { sum := sha1.Sum(bytes); return hex.EncodeToString(sum[:]) }
func write(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}
func installed(t *testing.T, e *Engine) string {
	t.Helper()
	dir := filepath.Join(e.dataDir, "servers", "test")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	jar := []byte("verified fixture jar")
	write(t, filepath.Join(dir, "server.jar"), jar)
	marker, _ := json.Marshal(installMarker{ServerID: "srv1", Software: "Vanilla", Version: "1.21", SHA1: digest(jar)})
	write(t, filepath.Join(dir, ".horizon-server.json"), marker)
	write(t, filepath.Join(dir, "server.properties"), []byte(fmt.Sprintf("motd=Keep me\nserver-ip=0.0.0.0\nserver-port=%d\n", freePort(t))))
	write(t, filepath.Join(dir, "eula.txt"), []byte("eula=true\n"))
	return dir
}
func TestRFC1918AndPortDetection(t *testing.T) {
	for _, text := range []string{"10.0.0.1", "172.16.0.1", "172.31.255.254", "192.168.3.8"} {
		if !isRFC1918(netip.MustParseAddr(text)) {
			t.Fatal(text)
		}
	}
	for _, text := range []string{"100.64.1.1", "172.15.0.1", "172.32.0.1", "8.8.8.8", "127.0.0.1", "fd00::1"} {
		if isRFC1918(netip.MustParseAddr(text)) {
			t.Fatal(text)
		}
	}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ok, err := checkPort("127.0.0.1", l.Addr().(*net.TCPAddr).Port)
	if err != nil || ok {
		t.Fatal("occupied port reported available")
	}
	if _, err = checkPort("bad", 25565); err == nil {
		t.Fatal("invalid address accepted")
	}
	if err = validateBinding("100.64.1.1", 25565); err == nil {
		t.Fatal("CGNAT binding accepted")
	}
}
func TestPrepareNeverOverwritesWorlds(t *testing.T) {
	e := testEngine(t)
	if _, err := e.prepareDirectory("relative"); err == nil {
		t.Fatal("relative path accepted")
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "level.dat"), []byte("keep"))
	if _, err := e.prepareDirectory(dir); err == nil {
		t.Fatal("nonempty folder accepted")
	}
	bytes, _ := os.ReadFile(filepath.Join(dir, "level.dat"))
	if string(bytes) != "keep" {
		t.Fatal("world changed")
	}
	for _, id := range []string{"", "../escape", "x/y", "hello\n"} {
		if safeID(id) {
			t.Fatal("bad id accepted")
		}
	}
}
func TestIdentityAndChecksum(t *testing.T) {
	e := testEngine(t)
	dir := installed(t, e)
	root, _ := os.OpenRoot(dir)
	defer root.Close()
	if err := verifyInstalled(root, "srv1"); err != nil {
		t.Fatal(err)
	}
	if err := verifyInstalled(root, "other"); err == nil {
		t.Fatal("wrong identity accepted")
	}
	write(t, filepath.Join(dir, "server.jar"), []byte("changed"))
	if err := verifyInstalled(root, "srv1"); err == nil {
		t.Fatal("changed jar accepted")
	}
}
func TestPreferencesAreDurableAndCorruptDataIsPreserved(t *testing.T) {
	e := testEngine(t)
	if err := e.writePreferences(map[string]string{"horizon.theme": "light"}); err != nil {
		t.Fatal(err)
	}
	e2, err := New(Options{DataDir: e.dataDir})
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close(context.Background())
	values, err := e2.readPreferences()
	if err != nil || values["horizon.theme"] != "light" {
		t.Fatal(values, err)
	}
	if err = e.writePreferences(map[string]string{"arbitrary": "bad"}); err == nil {
		t.Fatal("unknown key accepted")
	}
	write(t, filepath.Join(e.dataDir, "preferences.json"), []byte("broken"))
	if err = e.writePreferences(map[string]string{"horizon.theme": "dark"}); err == nil {
		t.Fatal("corrupt preferences overwritten")
	}
	bytes, _ := os.ReadFile(filepath.Join(e.dataDir, "preferences.json"))
	if string(bytes) != "broken" {
		t.Fatal("corrupt file changed")
	}
}
func TestPropertiesUpdatePreservesUnrelatedSettings(t *testing.T) {
	e := testEngine(t)
	dir := installed(t, e)
	port := freePort(t)
	if err := e.setBinding("srv1", dir, "0.0.0.0", port); err != nil {
		t.Fatal(err)
	}
	bytes, _ := os.ReadFile(filepath.Join(dir, "server.properties"))
	if !strings.Contains(string(bytes), "motd=Keep me") {
		t.Fatal("unrelated property lost")
	}
	address, got, err := parseBinding(bytes)
	if err != nil || got != port || address != "0.0.0.0" {
		t.Fatal(address, got, err)
	}
	for _, bad := range []string{"server-port=25565", "server-ip=\nserver-port=25565", "server-ip=0.0.0.0\nserver-ip=10.0.0.1\nserver-port=25565"} {
		if _, _, err = parseBinding([]byte(bad)); err == nil {
			t.Fatal("ambiguous/implicit bind accepted")
		}
	}
}
func TestDownloadAllowlist(t *testing.T) {
	e := testEngine(t)
	for _, url := range []string{"http://piston-data.mojang.com/x", "https://evil.invalid/x", "https://piston-data.mojang.com.evil.invalid/x", "https://user@piston-data.mojang.com/x", "https://piston-data.mojang.com:444/x", "file:///tmp/x"} {
		if err := e.validateDownloadURL(url); err == nil {
			t.Fatal("unsafe URL accepted", url)
		}
	}
	if err := e.validateDownloadURL("https://piston-data.mojang.com/x"); err != nil {
		t.Fatal(err)
	}
}
func TestVerifiedInstallationAndChecksumFailureCleanup(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			e := testEngine(t)
			jar := []byte("official fixture server")
			var base string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				meta, _ := json.Marshal(map[string]any{"downloads": map[string]any{"server": map[string]any{"url": base + "/server.jar", "sha1": digest(jar), "size": len(jar)}}})
				switch r.URL.Path {
				case "/manifest":
					json.NewEncoder(w).Encode(versionManifest{Versions: []minecraftVersion{{ID: "1.21", Type: "release", URL: base + "/meta", SHA1: digest(meta)}}})
				case "/meta":
					w.Write(meta)
				case "/server.jar":
					if corrupt {
						w.Write(bytes.Repeat([]byte("x"), len(jar)))
					} else {
						w.Write(jar)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			base = server.URL
			e.manifest = base + "/manifest"
			e.allowTestHTTP = true
			versions, err := e.listVersions(context.Background())
			if err != nil || len(versions) != 1 {
				t.Fatal(versions, err)
			}
			dir := t.TempDir()
			p := InstallRequest{ServerID: "install1", Version: "1.21", Directory: dir, BindAddress: "0.0.0.0", Port: freePort(t)}
			if _, err = e.install(context.Background(), p); err == nil {
				t.Fatal("missing EULA accepted")
			}
			p.AcceptEULA = true
			result, err := e.install(context.Background(), p)
			if corrupt {
				if err == nil {
					t.Fatal("corrupt jar accepted")
				}
				entries, _ := os.ReadDir(dir)
				if len(entries) != 0 {
					t.Fatal("failed installation left files", entries)
				}
			} else {
				if err != nil || !result.SHA1Verified {
					t.Fatal(result, err)
				}
				root, _ := os.OpenRoot(dir)
				defer root.Close()
				if err = verifyInstalled(root, p.ServerID); err != nil {
					t.Fatal(err)
				}
				if _, err = e.install(context.Background(), p); err == nil {
					t.Fatal("existing installation overwritten")
				}
			}
		})
	}
}
func TestBackupRestoreAndExclusions(t *testing.T) {
	e := testEngine(t)
	dir := installed(t, e)
	os.MkdirAll(filepath.Join(dir, "world"), 0700)
	write(t, filepath.Join(dir, "world", "level.dat"), []byte("world-data"))
	os.Mkdir(filepath.Join(dir, "logs"), 0700)
	write(t, filepath.Join(dir, "logs", "latest.log"), []byte("skip"))
	write(t, filepath.Join(dir, "pending.download"), []byte("skip"))
	backup, err := e.backup(context.Background(), "srv1", dir)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(backup.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range archive.File {
		if strings.HasPrefix(entry.Name, "logs/") || strings.Contains(entry.Name, ".horizon-backups") || entry.Name == "pending.download" {
			t.Fatal("excluded entry included", entry.Name)
		}
	}
	archive.Close()
	p := RestoreRequest{ServerID: "srv1", ServerDirectory: dir, BackupPath: backup.Path, Destination: dir + "-restored-test", Confirmed: true}
	destination, err := e.restore(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	bytes, _ := os.ReadFile(filepath.Join(destination, "world", "level.dat"))
	if string(bytes) != "world-data" {
		t.Fatal("restore lost world")
	}
	if _, err = e.restore(context.Background(), p); err == nil {
		t.Fatal("existing restore overwritten")
	}
	p.Destination = dir
	if _, err = e.restore(context.Background(), p); err == nil {
		t.Fatal("source overwrite accepted")
	}
	p.Destination = dir + "-restored-other"
	p.BackupPath = filepath.Join(t.TempDir(), "foreign.zip")
	if _, err = e.restore(context.Background(), p); err == nil {
		t.Fatal("foreign archive accepted")
	}
}
func TestRejectsUnsafeArchivesBeforeExtraction(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "C:/drive", "world\\..\\escape", "world/file:stream", "world/CON", "a/./b", "a//b", "a./b", "NUL.txt", "world/a ", "x\x00y"} {
		if _, err := validateArchiveName(name); err == nil {
			t.Fatal("unsafe name accepted", name)
		}
	}
	for _, names := range [][]string{{"World/a", "world/A"}, {"world", "world/file"}, {"world/file", "world"}} {
		var files []*zip.File
		for _, name := range names {
			files = append(files, &zip.File{FileHeader: zip.FileHeader{Name: name}})
		}
		if err := validateArchive(files); err == nil {
			t.Fatal("collision accepted", names)
		}
	}
	symbolic := &zip.File{FileHeader: zip.FileHeader{Name: "link"}}
	symbolic.SetMode(os.ModeSymlink | 0777)
	if validateArchive([]*zip.File{symbolic}) == nil {
		t.Fatal("symlink accepted")
	}
	if validateArchive([]*zip.File{{FileHeader: zip.FileHeader{Name: "big", UncompressedSize64: maxExpandedBytes + 1}}}) == nil {
		t.Fatal("zip bomb accepted")
	}
	e := testEngine(t)
	dir := installed(t, e)
	os.Mkdir(filepath.Join(dir, ".horizon-backups"), 0700)
	archive := filepath.Join(dir, ".horizon-backups", "unsafe.zip")
	f, _ := os.Create(archive)
	z := zip.NewWriter(f)
	w, _ := z.Create("../outside.txt")
	w.Write([]byte("bad"))
	z.Close()
	f.Close()
	destination := dir + "-restored-unsafe"
	if _, err := e.restore(context.Background(), RestoreRequest{ServerID: "srv1", ServerDirectory: dir, BackupPath: archive, Destination: destination, Confirmed: true}); err == nil {
		t.Fatal("traversal accepted")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("unsafe restore created destination")
	}
}
func TestAndroidCannotRunDesktopJavaOrEscapeStorage(t *testing.T) {
	e := testEngine(t)
	e.platform = "android"
	if _, err := e.start(StartRequest{ServerID: "srv1"}); err == nil || !strings.Contains(err.Error(), "Android JVM") {
		t.Fatal("Android pretended to host", err)
	}
	if _, err := e.prepareDirectory(t.TempDir()); err == nil {
		t.Fatal("external Android path accepted")
	}
}
func TestProcessLifecycleOutputAndStoppedGuards(t *testing.T) {
	t.Setenv("HORIZON_TEST_JAVA", "1")
	e := testEngine(t)
	dir := installed(t, e)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	status, err := e.start(StartRequest{ServerID: "srv1", Directory: dir, MemoryMB: 512, JavaPath: &exe})
	if err != nil || !status.Running || status.PID == nil {
		t.Fatal(status, err)
	}
	if _, err = e.start(StartRequest{ServerID: "srv1", Directory: dir, MemoryMB: 512, JavaPath: &exe}); err == nil {
		t.Fatal("duplicate start accepted")
	}
	if _, err = e.backup(context.Background(), "srv1", dir); err == nil {
		t.Fatal("live backup accepted")
	}
	if err = e.setBinding("srv1", dir, "0.0.0.0", freePort(t)); err == nil {
		t.Fatal("live binding edit accepted")
	}
	if err = e.sendCommand("srv1", "say hi\nstop"); err == nil {
		t.Fatal("multiline command accepted")
	}
	if err = e.sendCommand("srv1", "say hello"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		for _, event := range e.events.poll(0).Events {
			if event.Name == "server-output" && strings.Contains(fmt.Sprint(event.Payload), "received: say hello") {
				found = true
			}
		}
		if found {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !found {
		t.Fatal("no process output event")
	}
	status, err = e.stop(context.Background(), "srv1")
	if err != nil || status.Running || status.ExitCode == nil || *status.ExitCode != 0 {
		t.Fatal(status, err)
	}
	if err = e.sendCommand("srv1", "say after stop"); err == nil {
		t.Fatal("stopped command accepted")
	}
}
func TestCrashStatusAndBoundedForcedStop(t *testing.T) {
	t.Setenv("HORIZON_TEST_JAVA", "1")
	e := testEngine(t)
	dir := installed(t, e)
	exe, _ := os.Executable()
	if _, err := e.start(StartRequest{ServerID: "srv1", Directory: dir, MemoryMB: 512, JavaPath: &exe}); err != nil {
		t.Fatal(err)
	}
	e.sendCommand("srv1", "crash")
	deadline := time.Now().Add(5 * time.Second)
	for e.status("srv1").Running && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	status := e.status("srv1")
	if status.Running || status.ExitCode == nil || *status.ExitCode != 7 {
		t.Fatal(status)
	}
	t.Setenv("HORIZON_TEST_IGNORE_STOP", "1")
	if _, err := e.start(StartRequest{ServerID: "srv1", Directory: dir, MemoryMB: 512, JavaPath: &exe}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	status, err := e.stop(ctx, "srv1")
	if err != nil || status.Running {
		t.Fatal(status, err)
	}
}
func TestEventQueueBoundedAndResynchronizes(t *testing.T) {
	var bus eventBus
	for i := 0; i < 2100; i++ {
		bus.publish("test", i)
	}
	batch := bus.poll(0)
	if !batch.Dropped || len(batch.Events) != 500 {
		t.Fatal(batch)
	}
	if bus.poll(99999).Cursor != 0 {
		t.Fatal("cursor failed to reset")
	}
}
