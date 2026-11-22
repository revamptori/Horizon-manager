package core

import (
	"crypto/rand"
	"crypto/sha1" // Mojang publishes SHA-1 for integrity, not for signatures or authentication.
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func safeID(id string) bool { return idPattern.MatchString(id) }
func randomSuffix() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func within(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (e *Engine) directory(path string, create bool) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("use an absolute server directory path")
	}
	path = filepath.Clean(path)
	if filepath.Dir(path) == path {
		return "", errors.New("a filesystem root cannot be used as a server directory")
	}
	// For Android, app-private storage is intentional. Storage Access Framework URIs are not filesystem paths.
	if e.platform == "android" && !within(filepath.Join(e.dataDir, "servers"), path) {
		return "", errors.New("Android server folders must stay inside Horizon's app-private servers directory")
	}
	if create {
		if err := os.MkdirAll(path, 0700); err != nil {
			return "", err
		}
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("the chosen server path is not a directory")
	}
	if e.platform == "android" && !within(filepath.Join(e.dataDir, "servers"), canonical) {
		return "", errors.New("Android server folder resolves outside app-private storage")
	}
	return canonical, nil
}
func (e *Engine) defaultDirectory(name string) (string, error) {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 64 {
		slug = slug[:64]
	}
	if slug == "" {
		return "", errors.New("enter a server name to choose its folder")
	}
	// Prefixing avoids Windows reserved names such as CON, AUX, and COM1.
	return filepath.Join(e.dataDir, "servers", "server-"+slug), nil
}
func (e *Engine) prepareDirectory(path string) (string, error) {
	dir, err := e.directory(strings.TrimSpace(path), true)
	if err != nil {
		return "", err
	}
	unlock := e.lockDirectory(dir)
	defer unlock()
	if err = emptyDirectory(dir); err != nil {
		return "", err
	}
	return dir, nil
}
func emptyDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("the chosen directory is not empty; existing files and worlds will not be overwritten")
}
func readRegular(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file; links and devices are not accepted", name)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s exceeds its safety size limit", name)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	bytes, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(bytes)) > limit {
		err = errors.New("file grew beyond its safety size limit")
	}
	return bytes, err
}
func writeExclusive(root *os.Root, name string, bytes []byte) (err error) {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			root.Remove(name)
		}
	}()
	_, err = f.Write(bytes)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

type installMarker struct {
	ServerID string `json:"serverId"`
	Software string `json:"software"`
	Version  string `json:"version"`
	SHA1     string `json:"sha1"`
}

func verifyInstalled(root *os.Root, id string) error {
	if !safeID(id) {
		return errors.New("invalid local server identifier")
	}
	bytes, err := readRegular(root, ".horizon-server.json", 8192)
	if err != nil {
		return fmt.Errorf("not a verified Horizon installation: %w", err)
	}
	var marker installMarker
	if err = json.Unmarshal(bytes, &marker); err != nil {
		return err
	}
	if marker.ServerID != id || marker.Software != "Vanilla" || len(marker.SHA1) != 40 {
		return errors.New("server identity does not match the verified installation record")
	}
	info, err := root.Lstat("server.jar")
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxDownload {
		return errors.New("server.jar is not a valid regular server file")
	}
	f, err := root.Open("server.jar")
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha1.New()
	n, err := io.Copy(hash, io.LimitReader(f, maxDownload+1))
	if err != nil {
		return err
	}
	if n > maxDownload || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), marker.SHA1) {
		return errors.New("server.jar no longer matches the verified Mojang checksum; refusing the operation")
	}
	return nil
}
