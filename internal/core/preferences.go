package core

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"
)

var preferenceKeys = map[string]bool{"horizon.server-profiles.v1": true, "horizon.activity.v1": true, "horizon.theme": true, "horizon.default-memory": true}

func (e *Engine) readPreferences() (map[string]string, error) {
	e.preferencesMu.Lock()
	defer e.preferencesMu.Unlock()
	return e.readPreferencesLocked()
}
func (e *Engine) readPreferencesLocked() (map[string]string, error) {
	root, err := os.OpenRoot(e.dataDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	bytes, err := readRegular(root, "preferences.json", 2<<20)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var values map[string]string
	if err = json.Unmarshal(bytes, &values); err != nil {
		return nil, errors.New("saved preferences are invalid; preserved preferences.json without overwriting it")
	}
	if values == nil {
		values = map[string]string{}
	}
	return values, nil
}
func (e *Engine) writePreferences(patch map[string]string) error {
	for key, value := range patch {
		if !preferenceKeys[key] {
			return errors.New("unknown preference key")
		}
		if len(value) > 1<<20 {
			return errors.New("preference value exceeds the 1 MB safety limit")
		}
	}
	e.preferencesMu.Lock()
	defer e.preferencesMu.Unlock()
	values, err := e.readPreferencesLocked()
	if err != nil {
		return err
	}
	for k, v := range patch {
		values[k] = v
	}
	return e.writePreferencesLocked(values)
}

func (e *Engine) writePreferencesLocked(values map[string]string) error {
	bytes, err := json.Marshal(values)
	if err != nil {
		return err
	}
	if len(bytes) > 2<<20 {
		return errors.New("preferences exceed the 2 MB safety limit")
	}
	root, err := os.OpenRoot(e.dataDir)
	if err != nil {
		return err
	}
	defer root.Close()
	temp := ".preferences-" + randomSuffix()
	if err = writeExclusive(root, temp, bytes); err != nil {
		return err
	}
	defer root.Remove(temp)
	return root.Rename(temp, "preferences.json")
}

// Save the installed profile before returning across JNI/HTTP. A closed tab or
// destroyed Activity must not make a completed background install undiscoverable.
func (e *Engine) rememberInstallation(p InstallRequest, dir string) error {
	e.preferencesMu.Lock()
	defer e.preferencesMu.Unlock()
	values, err := e.readPreferencesLocked()
	if err != nil {
		return err
	}
	var profiles []map[string]any
	if value := values["horizon.server-profiles.v1"]; value != "" {
		if err = json.Unmarshal([]byte(value), &profiles); err != nil {
			return err
		}
	}
	for _, profile := range profiles {
		if profile["id"] == p.ServerID {
			return errors.New("a profile already uses this server identifier")
		}
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = "Vanilla " + p.Version
	}
	profile := map[string]any{"id": p.ServerID, "name": name, "directory": dir, "software": "Vanilla", "version": p.Version, "port": p.Port, "bindAddress": p.BindAddress, "memoryMb": p.MemoryMB, "installed": true, "createdAt": time.Now().UTC().Format(time.RFC3339Nano), "backups": []BackupInfo{}}
	profiles = append([]map[string]any{profile}, profiles...)
	bytes, err := json.Marshal(profiles)
	if err != nil {
		return err
	}
	values["horizon.server-profiles.v1"] = string(bytes)
	if len(bytes) > 1<<20 {
		return errors.New("server profile storage exceeds the 1 MB safety limit")
	}
	return e.writePreferencesLocked(values)
}
