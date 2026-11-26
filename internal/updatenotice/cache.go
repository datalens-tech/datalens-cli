package updatenotice

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Notify(dir, current string, out io.Writer, launch func() error) {
	if dir == "" || !ValidVersion(current) {
		return
	}
	channel := channelDir(dir, current)
	if err := os.MkdirAll(channel, 0700); err != nil {
		return
	}
	latest := readLatest(channel)
	if Newer(current, latest) {
		marker := filepath.Join(dir, "shown-"+latest)
		if claim(marker) {
			_, err := fmt.Fprintf(out, "DataLens CLI %s is available (you have %s). Download: https://github.com/datalens-tech/datalens-cli/releases/tag/v%s\n", latest, strings.TrimPrefix(current, "v"), latest)
			if err != nil {
				os.Remove(marker)
			}
		}
	}
	if claimDailyCheck(channel, time.Now().UTC()) {
		_ = launch()
	}
}

func Refresh(ctx context.Context, dir, current string, client *http.Client) {
	if dir == "" || !ValidVersion(current) {
		return
	}
	latest, err := Latest(ctx, client, current)
	if err != nil {
		return
	}
	channel := channelDir(dir, current)
	temp, err := os.CreateTemp(channel, ".latest-*")
	if err != nil {
		return
	}
	defer os.Remove(temp.Name())
	_, err = io.WriteString(temp, latest)
	closeErr := temp.Close()
	if err == nil && closeErr == nil {
		_ = os.Rename(temp.Name(), filepath.Join(channel, "latest"))
	}
}

func channelDir(dir, version string) string {
	if strings.Contains(version, "-beta.") {
		return filepath.Join(dir, "beta")
	}
	return filepath.Join(dir, "stable")
}

func readLatest(dir string) string {
	path := filepath.Join(dir, "latest")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024 {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(releaseTag(string(data)), "v")
}

func claim(path string) bool {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return false
	}
	return f.Close() == nil
}

func claimDailyCheck(dir string, now time.Time) bool {
	today := "check-" + now.Format(time.DateOnly)
	path := filepath.Join(dir, today)
	if !claim(path) {
		return false
	}
	// Overlapping processes can claim adjacent dates on opposite sides of midnight.
	for _, offset := range []time.Duration{-24 * time.Hour, 24 * time.Hour} {
		adjacent := "check-" + now.Add(offset).Format(time.DateOnly)
		if info, err := os.Stat(filepath.Join(dir, adjacent)); err == nil && now.Sub(info.ModTime()) < 24*time.Hour {
			os.Remove(path)
			return false
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "check-") && entry.Name() < today {
			os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	return true
}
