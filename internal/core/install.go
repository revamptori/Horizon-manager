package core

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type minecraftVersion struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	ReleaseTime string `json:"releaseTime"`
	URL         string `json:"url"`
	SHA1        string `json:"sha1"`
}
type versionManifest struct {
	Versions []minecraftVersion `json:"versions"`
}
type VersionInfo struct {
	ID          string `json:"id"`
	VersionType string `json:"versionType"`
	ReleaseTime string `json:"releaseTime"`
}
type InstallRequest struct {
	Name        string `json:"name"`
	MemoryMB    int    `json:"memoryMb"`
	ServerID    string `json:"serverId"`
	Version     string `json:"version"`
	Directory   string `json:"directory"`
	BindAddress string `json:"bindAddress"`
	Port        int    `json:"port"`
	AcceptEULA  bool   `json:"acceptEula"`
}
type InstallResult struct {
	JarPath         string `json:"jarPath"`
	Version         string `json:"version"`
	BytesDownloaded int64  `json:"bytesDownloaded"`
	SHA1Verified    bool   `json:"sha1Verified"`
}

var versionPattern = regexp.MustCompile(`^[A-Za-z0-9.-]{1,32}$`)

func (e *Engine) validateDownloadURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if e.allowTestHTTP && u.Scheme == "http" && u.Hostname() == "127.0.0.1" {
		return nil
	}
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return errors.New("downloads must use official Mojang HTTPS URLs")
	}
	switch u.Hostname() {
	case "piston-meta.mojang.com", "piston-data.mojang.com", "launchermeta.mojang.com", "launcher.mojang.com":
		return nil
	}
	return errors.New("download URL is not on an allowed Mojang host")
}
func (e *Engine) get(ctx context.Context, raw string) (*http.Response, error) {
	if err := e.validateDownloadURL(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Horizon-Server-Manager/"+Version)
	response, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("Mojang returned HTTP %d", response.StatusCode)
	}
	return response, nil
}
func (e *Engine) getBytes(ctx context.Context, url string, limit int64) ([]byte, error) {
	response, err := e.get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.ContentLength > limit {
		return nil, errors.New("metadata exceeds its safety size limit")
	}
	bytes, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(bytes)) > limit {
		return nil, errors.New("metadata exceeds its safety size limit")
	}
	return bytes, nil
}
func (e *Engine) manifestData(ctx context.Context) (versionManifest, error) {
	var manifest versionManifest
	bytes, err := e.getBytes(ctx, e.manifest, 8<<20)
	if err != nil {
		return manifest, err
	}
	if err = json.Unmarshal(bytes, &manifest); err != nil {
		return manifest, fmt.Errorf("invalid Mojang version manifest: %w", err)
	}
	if len(manifest.Versions) == 0 {
		return manifest, errors.New("Mojang returned an empty version manifest")
	}
	return manifest, nil
}
func (e *Engine) listVersions(ctx context.Context) ([]VersionInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	manifest, err := e.manifestData(ctx)
	if err != nil {
		return nil, err
	}
	result := []VersionInfo{}
	for _, v := range manifest.Versions {
		if v.Type == "release" {
			result = append(result, VersionInfo{ID: v.ID, VersionType: v.Type, ReleaseTime: v.ReleaseTime})
			if len(result) == 30 {
				break
			}
		}
	}
	return result, nil
}
func (e *Engine) install(ctx context.Context, p InstallRequest) (result InstallResult, err error) {
	if p.MemoryMB == 0 {
		p.MemoryMB = 2048
	}
	if p.MemoryMB < 512 || p.MemoryMB > 32768 || len(p.Name) > 192 {
		return result, errors.New("invalid profile memory or name")
	}
	if !safeID(p.ServerID) {
		return result, errors.New("invalid local server identifier")
	}
	if !p.AcceptEULA {
		return result, errors.New("explicitly accept the Minecraft EULA before installing")
	}
	if !versionPattern.MatchString(p.Version) {
		return result, errors.New("invalid Minecraft release identifier")
	}
	dir, err := e.directory(p.Directory, false)
	if err != nil {
		return result, err
	}
	unlock := e.lockDirectory(dir)
	defer unlock()
	if err = emptyDirectory(dir); err != nil {
		return result, err
	}
	if err = validateBinding(p.BindAddress, p.Port); err != nil {
		return result, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return result, err
	}
	defer root.Close()
	manifest, err := e.manifestData(ctx)
	if err != nil {
		return result, err
	}
	var entry *minecraftVersion
	for i := range manifest.Versions {
		if manifest.Versions[i].ID == p.Version && manifest.Versions[i].Type == "release" {
			entry = &manifest.Versions[i]
			break
		}
	}
	if entry == nil {
		return result, errors.New("that release was not found in Mojang's official manifest")
	}
	metaBytes, err := e.getBytes(ctx, entry.URL, 2<<20)
	if err != nil {
		return result, err
	}
	if !validSHA1(metaBytes, entry.SHA1) {
		return result, errors.New("Mojang version metadata failed SHA-1 verification")
	}
	var metadata struct {
		Downloads struct {
			Server struct {
				URL  string `json:"url"`
				SHA1 string `json:"sha1"`
				Size int64  `json:"size"`
			} `json:"server"`
		} `json:"downloads"`
	}
	if err = json.Unmarshal(metaBytes, &metadata); err != nil {
		return result, err
	}
	download := metadata.Downloads.Server
	if download.URL == "" || len(download.SHA1) != 40 || download.Size <= 0 || download.Size > maxDownload {
		return result, errors.New("Mojang did not publish a verifiable server JAR within the 200 MB safety limit")
	}
	response, err := e.get(ctx, download.URL)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.ContentLength > maxDownload {
		return result, errors.New("server JAR exceeds the 200 MB safety limit")
	}
	temp := ".horizon-" + randomSuffix() + ".download"
	file, err := root.OpenFile(temp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return result, err
	}
	defer root.Remove(temp)
	defer file.Close()
	hash := sha1.New()
	writer := io.MultiWriter(file, hash)
	buffer := make([]byte, 64<<10)
	var downloaded int64
	started := time.Now()
	lastEvent := time.Time{}
	progress := func() {
		e.events.publish("installation-progress", map[string]any{"operation": "Downloading Vanilla server", "downloadedBytes": downloaded, "totalBytes": download.Size, "bytesPerSecond": int64(float64(downloaded) / max(time.Since(started).Seconds(), 0.001))})
	}
	for {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			downloaded += int64(n)
			if downloaded > maxDownload || downloaded > download.Size {
				return result, errors.New("server download exceeded its verified size limit")
			}
			if _, err = writer.Write(buffer[:n]); err != nil {
				return result, err
			}
			if time.Since(lastEvent) >= 100*time.Millisecond {
				progress()
				lastEvent = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return result, readErr
		}
	}
	progress()
	if downloaded != download.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), download.SHA1) {
		return result, errors.New("server JAR failed Mojang's size/SHA-1 verification; the download was discarded")
	}
	if err = file.Sync(); err != nil {
		return result, err
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return result, err
	}
	if len(entries) != 1 || entries[0].Name() != temp {
		return result, errors.New("the folder changed during installation; existing files were left untouched")
	}
	var created []string
	defer func() {
		if err != nil {
			for _, name := range created {
				root.Remove(name)
			}
		}
	}()
	jar, err := root.OpenFile("server.jar", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	created = append(created, "server.jar")
	if _, err = file.Seek(0, io.SeekStart); err == nil {
		_, err = io.Copy(jar, file)
	}
	if err == nil {
		err = jar.Sync()
	}
	closeErr := jar.Close()
	if err != nil {
		return result, err
	}
	if closeErr != nil {
		return result, closeErr
	}
	marker, _ := json.Marshal(installMarker{ServerID: p.ServerID, Software: "Vanilla", Version: p.Version, SHA1: download.SHA1})
	for _, item := range []struct {
		name string
		data []byte
	}{{"eula.txt", []byte("# Explicitly accepted in Horizon Server Manager\neula=true\n")}, {"server.properties", []byte(fmt.Sprintf("server-ip=%s\nserver-port=%d\n", p.BindAddress, p.Port))}, {".horizon-server.json", marker}} {
		if err = writeExclusive(root, item.name, item.data); err != nil {
			return result, err
		}
		created = append(created, item.name)
	}
	if err = e.rememberInstallation(p, dir); err != nil {
		return result, fmt.Errorf("could not persist installed profile: %w", err)
	}
	return InstallResult{JarPath: dir + string(os.PathSeparator) + "server.jar", Version: p.Version, BytesDownloaded: downloaded, SHA1Verified: true}, nil
}
func validSHA1(bytes []byte, expected string) bool {
	if len(expected) != 40 {
		return false
	}
	sum := sha1.Sum(bytes)
	return strings.EqualFold(hex.EncodeToString(sum[:]), expected)
}
