package core

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const maxArchiveEntries = 100_000
const maxExpandedBytes uint64 = 50 * 1024 * 1024 * 1024

type BackupInfo struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"sizeBytes"`
	CreatedAt string `json:"createdAt"`
}
type RestoreRequest struct {
	ServerID        string `json:"serverId"`
	ServerDirectory string `json:"serverDirectory"`
	BackupPath      string `json:"backupPath"`
	Destination     string `json:"destination"`
	Confirmed       bool   `json:"confirmed"`
}

func ignoredBackupPath(name string) bool {
	for _, part := range strings.Split(name, "/") {
		switch part {
		case "logs", "crash-reports", "cache", ".horizon-backups":
			return true
		}
	}
	for _, suffix := range []string{".tmp", ".download", ".horizon-tmp", ".horizon-backup"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
func (e *Engine) backup(ctx context.Context, id, directory string) (result BackupInfo, err error) {
	if !safeID(id) {
		return result, errors.New("invalid local server identifier")
	}
	dir, err := e.directory(directory, false)
	if err != nil {
		return result, err
	}
	unlock := e.lockDirectory(dir)
	defer unlock()
	if err = e.ensureStopped(id, dir); err != nil {
		return result, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return result, err
	}
	defer root.Close()
	if err = verifyInstalled(root, id); err != nil {
		return result, err
	}
	if err = root.Mkdir(".horizon-backups", 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return result, err
	}
	info, err := root.Lstat(".horizon-backups")
	if err != nil {
		return result, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return result, errors.New("Horizon backup folder must be a real directory, not a link")
	}
	now := time.Now().UTC()
	relative := ".horizon-backups/backup-" + now.Format("20060102-150405") + "-" + randomSuffix() + ".zip"
	file, err := root.OpenFile(relative, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return result, err
	}
	defer func() {
		file.Close()
		if err != nil {
			root.Remove(relative)
		}
	}()
	archive := zip.NewWriter(file)
	var total uint64
	count := 0
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if ignoredBackupPath(name) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return fmt.Errorf("cannot back up special file %s", name)
		}
		portableName := name
		if info.IsDir() {
			portableName += "/"
		}
		if _, err = validateArchiveName(portableName); err != nil {
			return err
		}
		count++
		if count > maxArchiveEntries {
			return errors.New("backup exceeds 100,000 entries")
		}
		if info.IsDir() {
			_, err = archive.CreateHeader(&zip.FileHeader{Name: portableName, Method: zip.Store})
			return err
		}
		size := uint64(info.Size())
		if size > maxExpandedBytes-total {
			return errors.New("backup exceeds the 50 GB expanded-size safety limit")
		}
		total += size
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = name
		header.Method = zip.Deflate
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		input, err := root.Open(name)
		if err != nil {
			return err
		}
		n, err := copyContext(ctx, writer, io.LimitReader(input, info.Size()+1))
		closeErr := input.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if n != info.Size() {
			return fmt.Errorf("%s changed during backup; retry with a stopped, unmodified server", name)
		}
		return nil
	})
	closeErr := archive.Close()
	if err != nil {
		return result, err
	}
	if closeErr != nil {
		return result, closeErr
	}
	if err = file.Sync(); err != nil {
		return result, err
	}
	info, err = file.Stat()
	if err != nil {
		return result, err
	}
	return BackupInfo{Path: filepath.Join(dir, filepath.FromSlash(relative)), SizeBytes: info.Size(), CreatedAt: now.Format(time.RFC3339Nano)}, nil
}

// validateArchiveName is deliberately portable, including Windows device/ADS defenses on Android/Linux.
func validateArchiveName(name string) (string, error) {
	invalid := func() (string, error) { return "", fmt.Errorf("unsafe archive path %q", name) }
	if !utf8.ValidString(name) || name == "" || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
		return invalid()
	}
	name = strings.TrimSuffix(name, "/")
	if name == "" {
		return invalid()
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" || component == "." || component == ".." || strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") {
			return invalid()
		}
		if strings.ContainsAny(component, "<>\"|?*") {
			return invalid()
		}
		for _, r := range component {
			if r < 32 {
				return invalid()
			}
		}
		stem := strings.ToUpper(strings.SplitN(component, ".", 2)[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CONIN$" || stem == "CONOUT$" || strings.HasPrefix(stem, "COM") && len(stem) == 4 && stem[3] >= '1' && stem[3] <= '9' || strings.HasPrefix(stem, "LPT") && len(stem) == 4 && stem[3] >= '1' && stem[3] <= '9' {
			return invalid()
		}
	}
	if path.Clean(name) != name {
		return invalid()
	}
	return name, nil
}
func validateArchive(files []*zip.File) error {
	if len(files) > maxArchiveEntries {
		return errors.New("backup contains too many entries")
	}
	seen := map[string]bool{}
	directories := map[string]bool{}
	var total uint64
	for _, file := range files {
		name, err := validateArchiveName(file.Name)
		if err != nil {
			return err
		}
		if !file.Mode().IsRegular() && !file.Mode().IsDir() {
			return errors.New("archive links and special files are not accepted")
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return errors.New("backup contains duplicate or case-colliding paths")
		}
		if directories[key] && !file.FileInfo().IsDir() {
			return errors.New("backup contains conflicting file/directory paths")
		}
		for parent := path.Dir(key); parent != "."; parent = path.Dir(parent) {
			if isDir, ok := seen[parent]; ok && !isDir {
				return errors.New("backup contains conflicting file/directory paths")
			}
			directories[parent] = true
		}
		seen[key] = file.FileInfo().IsDir()
		if file.UncompressedSize64 > maxExpandedBytes-total {
			return errors.New("backup expands beyond the 50 GB safety limit")
		}
		total += file.UncompressedSize64
	}
	return nil
}
func (e *Engine) restore(ctx context.Context, p RestoreRequest) (result string, err error) {
	if !p.Confirmed {
		return "", errors.New("confirm the restore operation before continuing")
	}
	if !safeID(p.ServerID) {
		return "", errors.New("invalid local server identifier")
	}
	dir, err := e.directory(p.ServerDirectory, false)
	if err != nil {
		return "", err
	}
	unlock := e.lockDirectory(dir)
	defer unlock()
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err = verifyInstalled(root, p.ServerID); err != nil {
		return "", err
	}
	backupDir, err := root.Lstat(".horizon-backups")
	if err != nil || !backupDir.IsDir() || backupDir.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("backup folder is unavailable or is a link")
	}
	if !filepath.IsAbs(p.BackupPath) {
		return "", errors.New("backup path must be absolute")
	}
	relative, err := filepath.Rel(dir, filepath.Clean(p.BackupPath))
	if err != nil || filepath.Dir(relative) != ".horizon-backups" {
		return "", errors.New("selected archive is not inside this server's Horizon backup folder")
	}
	info, err := root.Lstat(relative)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("backup must be a regular archive file, not a link")
	}
	file, err := root.Open(relative)
	if err != nil {
		return "", err
	}
	defer file.Close()
	archive, err := zip.NewReader(file, info.Size())
	if err != nil {
		return "", err
	}
	if err = validateArchive(archive.File); err != nil {
		return "", err
	}
	destination := filepath.Clean(p.Destination)
	if !filepath.IsAbs(destination) {
		return "", errors.New("restore destination must be absolute")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return "", err
	}
	if parent != filepath.Dir(dir) || !strings.HasPrefix(filepath.Base(destination), filepath.Base(dir)+"-restored-") {
		return "", errors.New("restore destination must be a new, clearly named sibling of the server directory")
	}
	destination = filepath.Join(parent, filepath.Base(destination))
	if err = os.Mkdir(destination, 0700); err != nil {
		return "", fmt.Errorf("restore requires a new folder; no existing folder is overwritten: %w", err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(destination)
		}
	}()
	output, err := os.OpenRoot(destination)
	if err != nil {
		return "", err
	}
	defer output.Close()
	for _, entry := range archive.File {
		if err = ctx.Err(); err != nil {
			return "", err
		}
		name, _ := validateArchiveName(entry.Name)
		if entry.FileInfo().IsDir() {
			if err = output.MkdirAll(name, 0700); err != nil {
				return "", err
			}
			continue
		}
		if parent := path.Dir(name); parent != "." {
			if err = output.MkdirAll(parent, 0700); err != nil {
				return "", err
			}
		}
		input, openErr := entry.Open()
		if openErr != nil {
			return "", openErr
		}
		target, createErr := output.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if createErr != nil {
			input.Close()
			return "", createErr
		}
		n, copyErr := copyContext(ctx, target, io.LimitReader(input, int64(entry.UncompressedSize64)+1))
		inputErr := input.Close()
		if copyErr == nil {
			copyErr = target.Sync()
		}
		closeErr := target.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if inputErr != nil {
			return "", inputErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		if uint64(n) != entry.UncompressedSize64 {
			return "", errors.New("archive expanded size differs from its declaration")
		}
	}
	if err = verifyInstalled(output, p.ServerID); err != nil {
		return "", fmt.Errorf("restored server identity/checksum is invalid: %w", err)
	}
	return destination, nil
}
func copyContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, 64<<10)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, readErr := source.Read(buffer)
		if n > 0 {
			m, writeErr := destination.Write(buffer[:n])
			written += int64(m)
			if writeErr != nil {
				return written, writeErr
			}
			if m != n {
				return written, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}
