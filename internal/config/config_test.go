package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error = %v; want code %s", err, code)
	}
}

func testStore(t *testing.T) Store {
	t.Helper()
	return Store{Dir: filepath.Join(t.TempDir(), "config")}
}

func TestPersistenceAndProfileIsolation(t *testing.T) {
	s := testStore(t)
	doc, err := s.Load()
	if err != nil || doc.Version != SchemaVersion || len(doc.Profiles) != 0 {
		t.Fatalf("initial document = %+v, %v", doc, err)
	}
	if _, err := os.Stat(s.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("loading missing config created a directory")
	}
	if err := s.Create(t.Context(), "team", Profile{Installation: "yateam"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(t.Context(), "cloud", Profile{Installation: "cloud", OrganizationID: "org"}); err != nil {
		t.Fatal(err)
	}
	doc, err = (Store{Dir: s.Dir}).Load()
	if err != nil {
		t.Fatal(err)
	}
	if doc.CurrentProfile != "team" {
		t.Fatalf("first profile should remain active: %q", doc.CurrentProfile)
	}
	if err := s.Use(t.Context(), "team"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete(t.Context(), "cloud"); err != nil {
		t.Fatal(err)
	}
	doc, err = s.Load()
	if err != nil || doc.CurrentProfile != "team" || len(doc.Profiles) != 1 {
		t.Fatalf("document = %+v, %v", doc, err)
	}
	if doc.Profiles["team"].APIEndpoint != teamAPI || doc.Profiles["team"].Endpoint != teamUI {
		t.Fatal("incorrect team endpoint")
	}
	if _, err := s.Delete(t.Context(), "team"); err != nil {
		t.Fatal(err)
	}
	doc, err = s.Load()
	if err != nil || doc.CurrentProfile != "" || len(doc.Profiles) != 0 {
		t.Fatalf("deleted document = %+v, %v", doc, err)
	}
	other, err := testStore(t).Load()
	if err != nil || len(other.Profiles) != 0 {
		t.Fatalf("isolated document = %+v, %v", other, err)
	}
}

func TestResolvePrecedenceAndNoFallback(t *testing.T) {
	doc := Document{Version: SchemaVersion, CurrentProfile: "saved", Profiles: map[string]Profile{
		"flag": {Installation: "yateam"}, "env": {Installation: "cloud", OrganizationID: "org"}, "saved": {Installation: "on-premises", Endpoint: "https://example.com"},
	}}
	for _, tc := range []struct{ flag, env, want string }{{"flag", "env", "flag"}, {"", "env", "env"}, {"", "", "saved"}} {
		name, p, err := Resolve(doc, tc.flag, tc.env)
		if err != nil || name != tc.want || p.Installation != doc.Profiles[tc.want].Installation {
			t.Fatalf("resolve = %q, %+v, %v", name, p, err)
		}
	}
	_, _, err := Resolve(doc, "missing", "env")
	requireCode(t, err, "PROFILE_NOT_FOUND")
	_, _, err = Resolve(doc, "", "missing")
	requireCode(t, err, "PROFILE_NOT_FOUND")
	for _, requested := range []string{"", defaultName, "missing"} {
		_, _, err := Resolve(emptyDocument(), requested, "")
		requireCode(t, err, "NO_PROFILES")
		if !strings.Contains(err.Error(), "datalens setup") || !strings.Contains(err.Error(), "datalens profiles create") {
			t.Fatalf("missing setup guidance in %q", err)
		}
	}
	_, _, err = Resolve(doc, defaultName, "env")
	requireCode(t, err, "PROFILE_NOT_FOUND")
}

func TestNormalizeProfile(t *testing.T) {
	for _, p := range []Profile{
		{Installation: "yateam"},
		{Installation: "cloud", OrganizationID: "org"},
		{Installation: "on-premises", Endpoint: "https://example.com:8443/api"},
		{Installation: "stackland", Endpoint: "https://[::1]/api"},
	} {
		got, err := NormalizeProfile(p)
		if err != nil || got.Endpoint == "" {
			t.Fatalf("normalize %+v = %+v, %v", p, got, err)
		}
	}
	for _, p := range []Profile{
		{Installation: "unknown"}, {Endpoint: "https://custom.example.com"}, {Endpoint: teamUI, Installation: "cloud"},
		{Endpoint: "https://datalens.ru:8443/."}, {Endpoint: teamUI + "/workbooks", Installation: "cloud"},
		{Installation: "on-premises"}, {Installation: "stackland"},
		{Installation: "yateam", OrganizationID: "org"},
		{Installation: "cloud", OrganizationID: "org\r\nheader: value"},
		{TokenCommand: []string{}}, {TokenCommand: []string{"", "iam"}}, {TokenCommand: []string{"  "}},
		{TokenCommand: []string{"yc", "arg\x00here"}}, {TokenCommand: []string{"yc", "line\nbreak"}},
		{TokenCommand: []string{"\xff"}},
	} {
		_, err := NormalizeProfile(p)
		requireCode(t, err, "PROFILE_INVALID")
	}
	for _, endpoint := range []string{"http://example.com", "https://", "//example.com", "https://user:secret@example.com", "https://example.com?token=secret", "https://example.com?", "https://example.com#fragment", "https://example.com#", "https://example.com:99999", "https://example.com:0", "https://example.com:", "https://example.com:invalid"} {
		for _, p := range []Profile{{Installation: "on-premises", Endpoint: endpoint}, {APIEndpoint: endpoint}} {
			_, err := NormalizeProfile(p)
			requireCode(t, err, "PROFILE_INVALID")
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("validation error exposed endpoint credentials")
			}
		}
	}
}

func TestResolveEndpoints(t *testing.T) {
	for _, tc := range []struct {
		input                     Profile
		wantType, wantUI, wantAPI string
	}{
		{Profile{}, "cloud", cloudUI, cloudAPI},
		{Profile{Installation: "yateam"}, "yateam", teamUI, teamAPI},
		{Profile{Endpoint: "datalens.ru"}, "cloud", cloudUI, cloudAPI},
		{Profile{Endpoint: cloudUI + "/."}, "cloud", cloudUI + "/.", cloudAPI},
		{Profile{Endpoint: "https://DATALENS.YANDEX-TEAM.RU:443/workbooks/"}, "yateam", teamUI + "/workbooks", teamAPI},
		{Profile{Endpoint: "https://DATALENS.YANDEX-TEAM.RU:443/"}, "yateam", teamUI, teamAPI},
		{Profile{Endpoint: cloudUI + "/workbooks", APIEndpoint: "https://override.example.com/v1/"}, "cloud", cloudUI + "/workbooks", "https://override.example.com/v1"},
		{Profile{Installation: "stackland", Endpoint: "https://datalens.ru:8443/prefix"}, "stackland", "https://datalens.ru:8443/prefix", "https://datalens.ru:8443/prefix/api"},
		{Profile{Installation: "on-premises", Endpoint: "https://corp.example.com:8443/prefix/"}, "on-premises", "https://corp.example.com:8443/prefix", "https://corp.example.com:8443/prefix/api"},
		{Profile{Installation: "stackland", Endpoint: "https://[FE80::A%25Eth0]:443/prefix/", APIEndpoint: "https://[FE80::B%25WiFi]:8443/"}, "stackland", "https://[fe80::a%25Eth0]/prefix", "https://[fe80::b%25WiFi]:8443"},
		{Profile{Installation: "stackland", Endpoint: "https://corp.example.com", APIEndpoint: "https://api.corp.example.com"}, "stackland", "https://corp.example.com", "https://api.corp.example.com"},
	} {
		got, err := NormalizeProfile(tc.input)
		if err != nil || got.Installation != tc.wantType || got.Endpoint != tc.wantUI || got.APIEndpoint != tc.wantAPI {
			t.Errorf("normalize %+v = %+v, %v", tc.input, got, err)
		}
	}
}

func TestDeleteActiveProfile(t *testing.T) {
	s := testStore(t)
	requireCode(t, s.Use(t.Context(), defaultName), "NO_PROFILES")
	for _, name := range []string{"team", "zulu", "alpha", defaultName} {
		if err := s.Create(t.Context(), name, Profile{Installation: "yateam"}); err != nil {
			t.Fatal(err)
		}
	}
	requireCode(t, s.Create(t.Context(), defaultName, Profile{}), "PROFILE_EXISTS")
	for _, tc := range []struct{ removed, active string }{
		{"team", "alpha"}, {"alpha", defaultName}, {defaultName, "zulu"}, {"zulu", ""},
	} {
		if empty, err := s.Delete(t.Context(), tc.removed); err != nil || empty != (tc.active == "") {
			t.Fatalf("delete %q: empty = %t, error = %v", tc.removed, empty, err)
		}
		doc, err := s.Load()
		if err != nil || doc.CurrentProfile != tc.active {
			t.Fatalf("delete %q: active = %q, error = %v", tc.removed, doc.CurrentProfile, err)
		}
		if _, exists := doc.Profiles[tc.removed]; exists {
			t.Fatalf("removed profile %q still exists", tc.removed)
		}
	}
	_, err := s.Delete(t.Context(), defaultName)
	requireCode(t, err, "NO_PROFILES")
}

func TestLoadLegacyActiveProfile(t *testing.T) {
	for _, tc := range []struct {
		current, profiles, want string
	}{
		{"default", "{}", ""},
		{"default", `{"z":{"installationType":"cloud"},"a":{"installationType":"yateam"}}`, "a"},
		{"", `{"a":{"installationType":"yateam"},"default":{"installationType":"cloud"}}`, "default"},
	} {
		s := testStore(t)
		if err := os.Mkdir(s.Dir, 0700); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(s.Dir, "config.json")
		raw := fmt.Sprintf(`{"version":2,"currentProfile":%q,"profiles":%s}`, tc.current, tc.profiles)
		if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		doc, err := s.Load()
		if err != nil || doc.CurrentProfile != tc.want {
			t.Fatalf("load %s: active = %q, error = %v", raw, doc.CurrentProfile, err)
		}
		content, err := os.ReadFile(file)
		if err != nil || string(content) != raw {
			t.Fatalf("loading legacy configuration modified it: %v", err)
		}
	}
}

func TestLoadRenamedOnPremisesProfile(t *testing.T) {
	s := testStore(t)
	if err := os.Mkdir(s.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	data := `{"version":2,"currentProfile":"corp","profiles":{"corp":{"installationType":"onpremise-standalone","endpoint":"https://bi.example.com"}}}`
	if err := os.WriteFile(filepath.Join(s.Dir, "config.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Load()
	if err != nil || doc.CurrentProfile != "corp" || doc.Profiles["corp"].Installation != "on-premises" {
		t.Fatalf("load legacy profile: %+v, %v", doc, err)
	}
}

func TestOrganizationIDRejectsASCIIControls(t *testing.T) {
	for c := rune(0); c <= 127; c++ {
		if c >= 32 && c != 127 {
			continue
		}
		for _, value := range []string{string(c) + "value", "value" + string(c), "va" + string(c) + "lue"} {
			_, err := NormalizeProfile(Profile{Installation: "cloud", OrganizationID: value})
			requireCode(t, err, "PROFILE_INVALID")
		}
	}
}

func TestInvalidMutationsPreserveConfiguration(t *testing.T) {
	s := testStore(t)
	if err := s.Create(t.Context(), "team", Profile{Installation: "yateam"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAndUse(t.Context(), "work", Profile{Installation: "cloud"}); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Load()
	if err != nil || doc.CurrentProfile != "work" || doc.Profiles["work"].APIEndpoint != cloudAPI {
		t.Fatalf("created and activated profile = %+v, %v", doc, err)
	}
	before, err := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, s.Create(t.Context(), "team", Profile{Installation: "yateam"}), "PROFILE_EXISTS")
	requireCode(t, s.CreateAndUse(t.Context(), "team", Profile{Installation: "cloud"}), "PROFILE_EXISTS")
	requireCode(t, s.Use(t.Context(), "missing"), "PROFILE_NOT_FOUND")
	_, err = s.Delete(t.Context(), "missing")
	requireCode(t, err, "PROFILE_NOT_FOUND")
	for _, name := range []string{"", ".", "..", "../escape", "name space", "name\nline"} {
		requireCode(t, s.Create(t.Context(), name, Profile{Installation: "yateam"}), "PROFILE_INVALID")
		requireCode(t, s.Use(t.Context(), name), "PROFILE_INVALID")
		_, err := s.Delete(t.Context(), name)
		requireCode(t, err, "PROFILE_INVALID")
	}
	after, err := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("invalid mutation changed config")
	}
}

func TestLegacyAPIVersionIsDroppedOnWrite(t *testing.T) {
	s := testStore(t)
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir, "config.json")
	const config = `{"version":2,"profiles":{"team":{"installationType":"yateam","apiVersion":"1"}}}`
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Use(t.Context(), "team"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"apiVersion"`) {
		t.Fatal("saved obsolete apiVersion")
	}
	doc, err := s.Load()
	if err != nil || doc.CurrentProfile != "team" || doc.Profiles["team"].Installation != "yateam" {
		t.Fatalf("updated document = %+v, %v", doc, err)
	}
}

func TestInvalidDocumentsArePreserved(t *testing.T) {
	for _, tc := range []struct{ content, code, hint string }{
		{`{"version":3,"profiles":{},"future":"secret"}`, "CONFIG_VERSION_UNSUPPORTED", "supports schema 3"},
		{`{"version":0,"profiles":{}}`, "CONFIG_VERSION_UNSUPPORTED", "datalens setup"},
		{`{"profiles":{}}`, "CONFIG_VERSION_UNSUPPORTED", "datalens setup"},
		{`{"version":1,"profiles":{"corp":{"installation":"standalone","endpoint":"https://corp.example.com/api"}}}`, "CONFIG_VERSION_UNSUPPORTED", "datalens setup"},
		{`{"version":2,"profiles":{`, "CONFIG_INVALID", ""},
		{`{"version":2,"profiles":{}} {"extra":true}`, "CONFIG_INVALID", ""},
		{`{"version":2,"profiles":null}`, "CONFIG_INVALID", ""},
		{`{"version":2,"profiles":{},"unknown":true}`, "CONFIG_INVALID", ""},
		{`{"version":2,"profiles":{"team":{"installation":"cloud"}}}`, "CONFIG_INVALID", ""},
		{`{"version":2,"profiles":{"team":{"installationType":"cloud","unknown":true}}}`, "CONFIG_INVALID", ""},
		{`{"version":2,"profiles":{"team":{"installationType":" \t "}}}`, "CONFIG_INVALID", ""},
		{`{"version":2,"profiles":{"team":{"installationType":"unknown"}}}`, "CONFIG_INVALID", "Installation type must be"},
		{`{"version":2,"profiles":{},"currentProfile":"missing"}`, "CONFIG_INVALID", "Set currentProfile"},
		{`{"version":2,"profiles":{"work":{"installationType":"cloud","endpoint":"https://datalens.ru?token=secret"}}}`, "CONFIG_INVALID", `profile "work"`},
		{`{"version":2,"profiles":{"work":{"installationType":"on-premises","serviceAccount":{"privateKeyFile":"secret"}}}}`, "CONFIG_INVALID", "serviceAccount.privateKeyFile"},
	} {
		t.Run(tc.content, func(t *testing.T) {
			s := testStore(t)
			if err := os.MkdirAll(s.Dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.Dir, "config.json")
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := s.Load()
			requireCode(t, err, tc.code)
			if !strings.Contains(err.Error(), tc.hint) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unexpected recovery message: %v", err)
			}
			requireCode(t, s.Create(t.Context(), "team", Profile{Installation: "yateam"}), tc.code)
			data, err := os.ReadFile(path)
			if err != nil || string(data) != tc.content {
				t.Fatal("invalid config was overwritten")
			}
		})
	}
}

func TestOversizedMutationPreservesConfiguration(t *testing.T) {
	s := testStore(t)
	if err := s.Create(t.Context(), "team", Profile{Installation: "yateam"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir, "config.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	large, err := NormalizeProfile(Profile{Installation: "cloud", OrganizationID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	doc.Profiles["large"] = large
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	large.OrganizationID = strings.Repeat("a", maxConfigSize-len(encoded)+1)
	err = s.Create(t.Context(), "large", large)
	requireCode(t, err, "CONFIG_INVALID")
	large.OrganizationID = strings.Repeat("a", maxConfigSize-len(encoded))
	requireCode(t, s.CreateAndUse(t.Context(), "large", large), "CONFIG_INVALID")
	after, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("oversized mutation changed config")
	}
	if err := s.Use(t.Context(), "team"); err != nil {
		t.Fatalf("configuration unusable after rejected mutation: %v", err)
	}
	doc, err = s.Load()
	if err != nil || len(doc.Profiles) != 1 || doc.CurrentProfile != "team" {
		t.Fatalf("document after rejected mutation = %+v, %v", doc, err)
	}
}

func TestExpiredMutationDoesNotCreateDirectory(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	mutations := []func() error{
		func() error { return s.Create(ctx, "team", Profile{}) },
		func() error { return s.Use(ctx, defaultName) },
		func() error { _, err := s.Delete(ctx, "team"); return err },
	}
	for _, mutate := range mutations {
		if err := mutate(); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("mutation error = %v, want deadline exceeded", err)
		}
	}
	if _, err := os.Stat(s.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("configuration directory after deadline: %v", err)
	}
}

func TestCancellationBeforeCommitPreservesConfiguration(t *testing.T) {
	s := testStore(t)
	if err := s.Create(t.Context(), "team", Profile{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := s.mutate(ctx, func(doc *Document) error {
		delete(doc.Profiles, "team")
		doc.CurrentProfile = ""
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("mutation error = %v, want canceled", err)
	}
	doc, err := s.Load()
	if err != nil || doc.CurrentProfile != "team" {
		t.Fatalf("stored default = %q, err = %v", doc.CurrentProfile, err)
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatalf("files after cancellation = %v, err = %v", entries, err)
	}
}

func TestRegularFileAsConfigDirectory(t *testing.T) {
	s := testStore(t)
	if err := os.WriteFile(s.Dir, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Load()
	requireCode(t, err, "CONFIG_UNSAFE_PATH")
	requireCode(t, s.Create(t.Context(), "team", Profile{}), "CONFIG_UNSAFE_PATH")
	data, err := os.ReadFile(s.Dir)
	if err != nil || string(data) != "keep" {
		t.Fatalf("file changed: %q, err = %v", data, err)
	}
}

func TestStoreNormalizesDirectory(t *testing.T) {
	s := testStore(t)
	sep := string(os.PathSeparator)
	spelled := Store{Dir: s.Dir + sep + "unused" + sep + ".." + sep + "." + sep}
	if err := spelled.Create(t.Context(), "team", Profile{}); err != nil {
		t.Fatal(err)
	}
	doc, err := spelled.Load()
	if _, found := doc.Profiles["team"]; err != nil || !found {
		t.Fatalf("loaded profiles = %v, err = %v", doc.Profiles, err)
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatalf("stored files = %v, err = %v", entries, err)
	}
}

func TestLockPreventsMutation(t *testing.T) {
	s := testStore(t)
	if err := s.Create(t.Context(), "team", Profile{Installation: "yateam"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(t.Context(), "second", Profile{}); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(s.Dir, ".config.lock")
	if err := os.WriteFile(lock, nil, 0600); err != nil {
		t.Fatal(err)
	}
	requireCode(t, s.Use(t.Context(), "second"), "CONFIG_LOCKED")
	doc, err := s.Load()
	if err != nil || doc.CurrentProfile != "team" {
		t.Fatal("locked mutation modified configuration")
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := s.Use(t.Context(), "second"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentStoresPreserveSuccessfulMutations(t *testing.T) {
	s := testStore(t)
	if err := s.Create(t.Context(), "initial", Profile{Installation: "yateam"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	type result struct {
		name string
		err  error
	}
	results := make(chan result, 12)
	for i := range 12 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("profile-%d", i)
			err := (Store{Dir: s.Dir}).Create(t.Context(), name, Profile{Installation: "yateam"})
			results <- result{name: name, err: err}
		}(i)
	}
	wg.Wait()
	close(results)
	doc, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := 1
	for result := range results {
		if result.err != nil {
			requireCode(t, result.err, "CONFIG_LOCKED")
			continue
		}
		want++
		if _, ok := doc.Profiles[result.name]; !ok {
			t.Errorf("successful mutation %s lost", result.name)
		}
	}
	if len(doc.Profiles) != want {
		t.Fatalf("profile count = %d; want %d", len(doc.Profiles), want)
	}
}

func TestPrivatePermissionsAndUnsafePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission and symlink contract")
	}
	s := testStore(t)
	if err := s.Create(t.Context(), "team", Profile{Installation: "yateam"}); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{s.Dir: 0700, filepath.Join(s.Dir, "config.json"): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("permissions for %s: %v, %v", path, info, err)
		}
	}
	t.Run("permissive file", func(t *testing.T) {
		path := filepath.Join(s.Dir, "config.json")
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(path, 0600)
		_, err := s.Load()
		requireCode(t, err, "CONFIG_UNSAFE_PATH")
		requireCode(t, s.Use(t.Context(), "team"), "CONFIG_UNSAFE_PATH")
	})
	t.Run("permissive directory", func(t *testing.T) {
		if err := os.Chmod(s.Dir, 0755); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(s.Dir, 0700)
		_, err := s.Load()
		requireCode(t, err, "CONFIG_UNSAFE_PATH")
		requireCode(t, s.Use(t.Context(), "team"), "CONFIG_UNSAFE_PATH")
	})
	t.Run("directory symlink", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(s.Dir, link); err != nil {
			t.Fatal(err)
		}
		linked := Store{Dir: link}
		_, err := linked.Load()
		requireCode(t, err, "CONFIG_UNSAFE_PATH")
		requireCode(t, linked.Use(t.Context(), "team"), "CONFIG_UNSAFE_PATH")
	})
	t.Run("file symlink", func(t *testing.T) {
		other := testStore(t)
		if err := os.MkdirAll(other.Dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(s.Dir, "config.json"), filepath.Join(other.Dir, "config.json")); err != nil {
			t.Fatal(err)
		}
		_, err := other.Load()
		requireCode(t, err, "CONFIG_UNSAFE_PATH")
		requireCode(t, other.Create(t.Context(), "other", Profile{Installation: "yateam"}), "CONFIG_UNSAFE_PATH")
	})
}
