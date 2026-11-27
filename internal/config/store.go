package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
)

type Store struct{ Dir string }

const maxConfigSize = 1024 * 1024

type SavedProfile struct {
	Profile
	invalid json.RawMessage
	reason  string
}

func (p SavedProfile) Problem(name string) string {
	if !ValidProfileName(name) {
		return "Rename this profile in config.json using letters, digits, dots, underscores, or hyphens."
	}
	if p.reason == "" {
		return ""
	}
	return fmt.Sprintf("Check profile %q in config.json. %s", name, p.reason)
}

func (p SavedProfile) MarshalJSON() ([]byte, error) {
	if p.invalid != nil {
		return p.invalid, nil
	}
	return json.Marshal(p.Profile)
}

func (p *SavedProfile) UnmarshalJSON(data []byte) error {
	var stored struct {
		Profile
		LegacyAPIVersion string `json:"apiVersion"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var reason string
	switch {
	case decoder.Decode(&stored) != nil:
		reason = "Correct the profile fields or delete and recreate it."
	case strings.TrimSpace(stored.Installation) == "":
		reason = "Set installationType for this profile."
	case stored.ServiceAccount != nil && !filepath.IsAbs(stored.ServiceAccount.PrivateKeyFile):
		reason = "Set serviceAccount.privateKeyFile to the absolute path of its PEM file."
	default:
		if stored.Installation == "onpremise-standalone" {
			stored.Installation = "on-premises"
		}
		profile, err := NormalizeProfile(stored.Profile)
		if err == nil {
			*p = SavedProfile{Profile: profile}
			return nil
		}
		reason = err.Error()
	}
	raw, err := json.Marshal(json.RawMessage(data))
	*p = SavedProfile{invalid: raw, reason: reason}
	return err
}

func emptyDocument() Document {
	return Document{Version: SchemaVersion, Profiles: map[string]SavedProfile{}}
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
	if version.Version > SchemaVersion {
		return Document{}, failure("CONFIG_VERSION_UNSUPPORTED", fmt.Sprintf("config.json uses schema %d. This CLI supports schema %d. Use the CLI version that wrote the file or upgrade to one that supports schema %d.", version.Version, SchemaVersion, version.Version))
	}
	if version.Version < SchemaVersion {
		return Document{}, failure("CONFIG_VERSION_UNSUPPORTED", "Cannot migrate this config.json automatically. Move it to a private backup. Recreate each profile with its saved settings using 'datalens setup' or 'datalens profiles create NAME'. For custom endpoints, pass --endpoint and --api-endpoint to 'profiles create'. Then run 'datalens profiles activate NAME' to restore the active profile.")
	}
	var doc Document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil || doc.Profiles == nil {
		return Document{}, failure("CONFIG_INVALID", "Configuration file has an invalid structure.")
	}
	if doc.CurrentProfile == "" && len(doc.Profiles) == 0 {
		return doc, nil
	}
	if doc.CurrentProfile == "" {
		doc.CurrentProfile = defaultName
	}
	if _, ok := doc.Profiles[doc.CurrentProfile]; !ok {
		if doc.CurrentProfile != defaultName {
			return Document{}, failure("CONFIG_INVALID", "Set currentProfile in config.json to the name of an existing profile.")
		}
		// Older configurations could select the built-in Cloud profile without saving it.
		doc.Profiles[defaultName] = SavedProfile{Profile: Profile{Installation: "cloud", Endpoint: cloudUI, APIEndpoint: cloudAPI}}
	}
	return doc, nil
}

func (s Store) Create(ctx context.Context, name string, profile Profile) error {
	return s.create(ctx, name, profile, false)
}

func (s Store) CreateAndUse(ctx context.Context, name string, profile Profile) error {
	return s.create(ctx, name, profile, true)
}

func (s Store) create(ctx context.Context, name string, profile Profile, activate bool) error {
	if !ValidProfileName(name) {
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
		doc.Profiles[name] = SavedProfile{Profile: p}
		if activate || len(doc.Profiles) == 1 {
			doc.CurrentProfile = name
		}
		return nil
	})
}

func (s Store) Use(ctx context.Context, name string) error {
	if !ValidProfileName(name) {
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

func (d Document) CheckDeletable(name string) error {
	if _, exists := d.Profiles[name]; exists {
		return nil
	}
	if !ValidProfileName(name) {
		return failure("PROFILE_INVALID", "A valid profile name is required.")
	}
	if len(d.Profiles) == 0 {
		return failure("NO_PROFILES", NoProfilesMessage)
	}
	return failure("PROFILE_NOT_FOUND", "The selected profile does not exist.")
}

func (s Store) Delete(ctx context.Context, name string, expected *SavedProfile) (empty bool, err error) {
	err = s.mutate(ctx, func(doc *Document) error {
		if err := doc.CheckDeletable(name); err != nil {
			return err
		}
		if expected != nil && !reflect.DeepEqual(*expected, doc.Profiles[name]) {
			return failure("PROFILE_CHANGED", "The profile changed while awaiting confirmation. Review its settings and retry the deletion.")
		}
		delete(doc.Profiles, name)
		if doc.CurrentProfile == name {
			doc.CurrentProfile = doc.ActiveName()
		}
		empty = len(doc.Profiles) == 0
		return nil
	})
	return empty && err == nil, err
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
