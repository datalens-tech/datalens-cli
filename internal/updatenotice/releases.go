package updatenotice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

var betaSuffix = regexp.MustCompile(`^-beta\.[0-9]+$`)

func releaseTag(version string) string {
	tag := "v" + strings.TrimPrefix(version, "v")
	if semver.Canonical(tag) != tag {
		return ""
	}
	if pre := semver.Prerelease(tag); pre != "" && !betaSuffix.MatchString(pre) {
		return ""
	}
	return tag
}

func ValidVersion(current string) bool {
	return releaseTag(current) != ""
}

func Newer(current, candidate string) bool {
	from, to := releaseTag(current), releaseTag(candidate)
	return eligible(from, to) && semver.Compare(to, from) > 0
}

func eligible(from, to string) bool {
	return from != "" && to != "" && (semver.Prerelease(from) != "" || semver.Prerelease(to) == "")
}

type release struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

func Latest(ctx context.Context, client *http.Client, current string) (string, error) {
	from := releaseTag(current)
	if from == "" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var newest string
	for page := 1; ; page++ {
		entries, more, err := releasePage(ctx, client, page)
		if err != nil {
			return "", err
		}
		for _, entry := range entries {
			tag := releaseTag(entry.Tag)
			if entry.Draft || entry.Tag != tag || !eligible(from, tag) {
				continue
			}
			if entry.Prerelease == (semver.Prerelease(tag) != "") && semver.Compare(tag, newest) > 0 {
				newest = tag
			}
		}
		if !more {
			break
		}
	}
	return strings.TrimPrefix(newest, "v"), nil
}

func releasePage(ctx context.Context, client *http.Client, page int) ([]release, bool, error) {
	url := fmt.Sprintf("https://api.github.com/repos/datalens-tech/datalens-cli/releases?per_page=100&page=%d", page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("GitHub Releases returned HTTP %d", resp.StatusCode)
	}
	const limit = 4 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > limit {
		return nil, false, fmt.Errorf("GitHub Releases response exceeds %d bytes", limit)
	}
	var entries []release
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, false, err
	}
	return entries, strings.Contains(resp.Header.Get("Link"), `rel="next"`), nil
}
