package core

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func property(line string) (key, value string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
		return "", ""
	}
	index := strings.IndexAny(line, "=: \t")
	if index < 0 {
		return line, ""
	}
	return line[:index], strings.TrimSpace(strings.TrimLeft(line[index:], " \t=:"))
}
func parseBinding(bytes []byte) (string, int, error) {
	var address, port string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(bytes), "\n") {
		key, value := property(line)
		if key == "server-ip" || key == "server-port" {
			if seen[key] {
				return "", 0, errors.New("duplicate network properties are ambiguous; repair server.properties first")
			}
			seen[key] = true
			if key == "server-ip" {
				address = value
			} else {
				port = value
			}
		}
	}
	if !seen["server-ip"] || address == "" {
		return "", 0, errors.New("network binding must be explicit; select a private interface or deliberately opt into 0.0.0.0")
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		return "", 0, errors.New("invalid server port in server.properties")
	}
	return address, number, nil
}
func (e *Engine) setBinding(id, path, address string, port int) error {
	if !safeID(id) {
		return errors.New("invalid local server identifier")
	}
	dir, err := e.directory(path, false)
	if err != nil {
		return err
	}
	unlock := e.lockDirectory(dir)
	defer unlock()
	if err = e.ensureStopped(id, dir); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = verifyInstalled(root, id); err != nil {
		return err
	}
	if err = validateBinding(address, port); err != nil {
		return err
	}
	existing, err := readRegular(root, "server.properties", 2<<20)
	if err != nil {
		return err
	}
	lines := []string{}
	for _, line := range strings.Split(strings.TrimRight(string(existing), "\n"), "\n") {
		key, _ := property(line)
		if key != "server-ip" && key != "server-port" {
			lines = append(lines, line)
		}
	}
	lines = append(lines, fmt.Sprintf("server-ip=%s", address), fmt.Sprintf("server-port=%d", port))
	data := []byte(strings.Join(lines, "\n") + "\n")
	backup := "server.properties.horizon-backup"
	if err = writeExclusive(root, backup, existing); err != nil {
		return fmt.Errorf("could not create recovery copy; resolve any existing %s first: %w", backup, err)
	}
	temp := ".horizon-properties-" + randomSuffix()
	if err = writeExclusive(root, temp, data); err != nil {
		return err
	}
	defer root.Remove(temp)
	if err = root.Rename(temp, "server.properties"); err != nil {
		return fmt.Errorf("configuration was not replaced; recovery copy was retained: %w", err)
	}
	return root.Remove(backup)
}
