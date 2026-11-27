package schemacache

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/datalens-tech/datalens-cli/internal/buildinfo"
	"github.com/datalens-tech/datalens-cli/internal/config"
	"github.com/datalens-tech/datalens-cli/internal/rpchelp"
)

const maxSchemaSize = 16 << 20

func location(dir string, profile config.Profile) (sources []string, path string, err error) {
	if dir == "" {
		return nil, "", errors.New("The schema cache requires a configuration directory.")
	}
	profile, err = config.NormalizeProfile(profile)
	if err != nil {
		return nil, "", err
	}
	base := profile.APIEndpoint
	suffix := "/" + buildinfo.APIVersion + "/json/"
	sources = []string{base + suffix}
	if profile.Installation == "on-premises" || profile.Installation == "stackland" {
		mounted := base + "/docs/swagger"
		if base == profile.Endpoint+"/api" {
			base = mounted
			sources[0] = mounted + suffix
		} else {
			sources = append(sources, mounted+suffix)
		}
	}
	name := fmt.Sprintf("schema-%s-v%s-%x.json", profile.Installation, buildinfo.APIVersion, sha256.Sum256([]byte(base)))
	return sources, filepath.Join(dir, name), nil
}

func Load(dir string, profile config.Profile) (*rpchelp.Catalog, error) {
	_, path, err := location(dir, profile)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("No local API schema is available for this profile.")
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSchemaSize || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("Cannot read the local API schema. Expected a private, regular file of at most 16 MiB.")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("Cannot open the local API schema.")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("The local API schema changed while opening it. Retry the command.")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSchemaSize+1))
	if err != nil {
		return nil, errors.New("Cannot read the local API schema.")
	}
	var catalog rpchelp.Catalog
	if len(data) > maxSchemaSize || json.Unmarshal(data, &catalog) != nil || len(catalog.Actions) == 0 {
		return nil, errors.New("The local API schema is invalid.")
	}
	return &catalog, nil
}

func Update(ctx context.Context, dir string, profile config.Profile, transport http.RoundTripper) error {
	sources, path, err := location(dir, profile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(url string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "datalens-cli/"+buildinfo.Version)
		req.Header.Set("x-dl-api-version", buildinfo.APIVersion)
		return client.Do(req)
	}
	resp, err := get(sources[0])
	if err == nil && resp.StatusCode == http.StatusNotFound && len(sources) == 2 {
		resp.Body.Close()
		resp, err = get(sources[1])
	}
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return errors.New("Cannot reach the API schema endpoint.")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("The API schema endpoint returned HTTP %d.", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSchemaSize+1))
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if err != nil {
		return errors.New("Cannot read the API schema response.")
	}
	if len(data) > maxSchemaSize {
		return errors.New("The API schema exceeds 16 MiB.")
	}
	catalog, err := rpchelp.ParseOpenAPI(data, buildinfo.APIVersion)
	if err != nil {
		return fmt.Errorf("The endpoint did not return a supported OpenAPI schema for DataLens API v%s.", buildinfo.APIVersion)
	}
	data, err = json.Marshal(catalog)
	if err != nil || len(data) > maxSchemaSize {
		return errors.New("Cannot encode the API schema within the 16 MiB cache limit.")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("Cannot create the schema cache directory.")
	}
	staged, err := os.CreateTemp(dir, ".schema-*")
	if err != nil {
		return errors.New("Cannot stage the local API schema.")
	}
	defer os.Remove(staged.Name())
	_, writeErr := staged.Write(data)
	closeErr := staged.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("Cannot write the local API schema.")
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if err := os.Rename(staged.Name(), path); err != nil {
		return errors.New("Cannot replace the local API schema.")
	}
	return nil
}
