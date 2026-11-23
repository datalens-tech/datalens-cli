package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Store struct{ Dir string }

const maxConfigSize = 1024 * 1024

func emptyDocument() Document {
	return Document{Version: SchemaVersion, Profiles: map[string]Profile{}}
}

func inspect(path string, directory bool) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory || (!directory && !info.Mode().IsRegular()) {
		return nil, failure("CONFIG_UNSAFE_PATH", "Configuration paths must be regular files and directories, not symbolic links.")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, failure("CONFIG_UNSAFE_PATH", "Configuration directory and files must be accessible only by their owner.")
	}
	return info, nil
}

func safeIO(err error, message string) error {
	var configErr *Error
	if errors.As(err, &configErr) {
		return configErr
	}
	return failure("CONFIG_IO", message)
}

func (s Store) directory() (string, error) {
	if s.Dir == "" {
		return "", failure("CONFIG_INVALID", "Configuration directory is required.")
	}
	return filepath.Clean(s.Dir), nil
}

func (s Store) Load() (Document, error) {
	dir, err := s.directory()
	if err != nil {
		return Document{}, err
	}
	if _, err := inspect(dir, true); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyDocument(), nil
		}
		return Document{}, safeIO(err, "Cannot access the configuration directory.")
	}
	path := filepath.Join(dir, "config.json")
	info, err := inspect(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return emptyDocument(), nil
	}
	if err != nil {
		return Document{}, safeIO(err, "Cannot access the configuration file.")
	}
	f, err := os.Open(path)
	if err != nil {
		return Document{}, failure("CONFIG_IO", "Cannot read the configuration file.")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return Document{}, failure("CONFIG_UNSAFE_PATH", "Configuration file changed while opening it; retry the command.")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxConfigSize+1))
	if err != nil {
		return Document{}, failure("CONFIG_IO", "Cannot read the configuration file.")
	}
	if len(data) > maxConfigSize {
		return Document{}, failure("CONFIG_INVALID", "Configuration file exceeds the size limit.")
	}
	var version struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return Document{}, failure("CONFIG_INVALID", "Configuration file is not valid JSON.")
	}
	if version.Version != SchemaVersion {
		return Document{}, failure("CONFIG_VERSION_UNSUPPORTED", "Configuration schema version is unsupported; use a compatible CLI version.")
	}
	var doc Document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil || doc.Profiles == nil {
		return Document{}, failure("CONFIG_INVALID", "Configuration file has an invalid structure.")
	}
	for name, profile := range doc.Profiles {
		if !validName(name) {
			return Document{}, failure("CONFIG_INVALID", "Configuration contains an invalid profile name.")
		}
		normalized, err := NormalizeProfile(profile)
		if err != nil || strings.TrimSpace(profile.Installation) == "" {
			return Document{}, failure("CONFIG_INVALID", "Configuration contains an invalid profile.")
		}
		doc.Profiles[name] = normalized
	}
	if doc.CurrentProfile != "" && doc.CurrentProfile != defaultName {
		if _, ok := doc.Profiles[doc.CurrentProfile]; !ok {
			return Document{}, failure("CONFIG_INVALID", "The default profile does not exist in the configuration.")
		}
	}
	return doc, nil
}

func (s Store) Create(ctx context.Context, name string, profile Profile) error {
	if !validName(name) {
		return failure("PROFILE_INVALID", "Profile names may contain only letters, digits, dots, underscores, and hyphens.")
	}
	p, err := NormalizeProfile(profile)
	if err != nil {
		return err
	}
	return s.mutate(ctx, func(doc *Document) error {
		if _, exists := doc.Profiles[name]; exists {
			return failure("PROFILE_EXISTS", "A profile with this name already exists.")
		}
		doc.Profiles[name] = p
		return nil
	})
}

func (s Store) Use(ctx context.Context, name string) error {
	if !validName(name) {
		return failure("PROFILE_INVALID", "A valid profile name is required.")
	}
	return s.mutate(ctx, func(doc *Document) error {
		if _, _, err := Resolve(*doc, name, ""); err != nil {
			return err
		}
		doc.CurrentProfile = name
		return nil
	})
}

func (s Store) SetOrganization(ctx context.Context, name, organization string) error {
	if !validName(name) {
		return failure("PROFILE_INVALID", "A valid profile name is required.")
	}
	return s.mutate(ctx, func(doc *Document) error {
		_, profile, err := Resolve(*doc, name, "")
		if err != nil {
			return err
		}
		profile.OrganizationID = organization
		profile, err = NormalizeProfile(profile)
		if err != nil {
			return err
		}
		doc.Profiles[name] = profile
		return nil
	})
}

func (s Store) Delete(ctx context.Context, name string) error {
	if !validName(name) {
		return failure("PROFILE_INVALID", "A valid profile name is required.")
	}
	return s.mutate(ctx, func(doc *Document) error {
		if _, exists := doc.Profiles[name]; !exists {
			if name == defaultName {
				return failure("PROFILE_IMPLICIT", "The built-in default profile has no saved settings to delete.")
			}
			return failure("PROFILE_NOT_FOUND", "The selected profile does not exist.")
		}
		delete(doc.Profiles, name)
		if doc.CurrentProfile == name {
			doc.CurrentProfile = ""
		}
		return nil
	})
}

func (s Store) mutate(ctx context.Context, change func(*Document) error) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	dir, err := s.directory()
	if err != nil {
		return err
	}
	_, err = inspect(dir, true)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return failure("CONFIG_IO", "Cannot create the configuration directory.")
		}
		_, err = inspect(dir, true)
	}
	if err != nil {
		return safeIO(err, "Cannot access the configuration directory.")
	}
	lockPath := filepath.Join(dir, ".config.lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return failure("CONFIG_LOCKED", "Configuration is locked by another command; if none is running, remove the stale .config.lock file.")
	}
	if err != nil {
		return failure("CONFIG_IO", "Cannot lock the configuration.")
	}
	defer os.Remove(lockPath)
	if err := lock.Close(); err != nil {
		return failure("CONFIG_IO", "Cannot lock the configuration.")
	}
	doc, err := s.Load()
	if err != nil {
		return err
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if err := change(&doc); err != nil {
		return err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return failure("CONFIG_IO", "Cannot encode the configuration.")
	}
	data = append(data, '\n')
	if len(data) > maxConfigSize {
		return failure("CONFIG_INVALID", "Configuration file exceeds the size limit.")
	}
	temp, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return failure("CONFIG_IO", "Cannot stage the configuration file.")
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if _, err := temp.Write(data); err != nil {
		return failure("CONFIG_IO", "Cannot write the configuration file.")
	}
	if err := temp.Sync(); err != nil {
		return failure("CONFIG_IO", "Cannot sync the configuration file.")
	}
	if err := temp.Close(); err != nil {
		return failure("CONFIG_IO", "Cannot close the configuration file.")
	}
	path := filepath.Join(dir, "config.json")
	if _, err := inspect(path, false); err != nil && !errors.Is(err, os.ErrNotExist) {
		return safeIO(err, "Cannot replace the configuration file.")
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return failure("CONFIG_IO", "Cannot replace the configuration file.")
	}
	return nil
}
