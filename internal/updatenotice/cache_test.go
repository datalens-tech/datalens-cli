package updatenotice

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type releaseResponse string

func (r releaseResponse) RoundTrip(*http.Request) (*http.Response, error) {
	if r == "" {
		return nil, errors.New("offline")
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(r)))}, nil
}

func TestConcurrentNoticeAndRefresh(t *testing.T) {
	dir := t.TempDir()
	const current = "0.1.0"
	var launches, notices atomic.Int32
	launch := func() error { launches.Add(1); return nil }
	var group sync.WaitGroup
	for range 20 {
		group.Go(func() { Notify(dir, current, io.Discard, launch) })
	}
	group.Wait()
	Refresh(context.Background(), dir, current, &http.Client{Transport: releaseResponse(`[{"tag_name":"v0.2.0"}]`)})
	Refresh(context.Background(), dir, current, &http.Client{Transport: releaseResponse("")})
	for range 20 {
		group.Go(func() {
			var out bytes.Buffer
			Notify(dir, current, &out, launch)
			if out.Len() > 0 {
				notices.Add(1)
				want := "DataLens CLI 0.2.0 is available (you have 0.1.0). Download: https://github.com/datalens-tech/datalens-cli/releases/tag/v0.2.0\n"
				if out.String() != want {
					t.Errorf("notice = %q", out.String())
				}
			}
		})
	}
	group.Wait()
	if notices.Load() != 1 || launches.Load() != 1 {
		t.Fatalf("notices = %d, launches = %d", notices.Load(), launches.Load())
	}
	Refresh(context.Background(), dir, current, &http.Client{Transport: releaseResponse(`[{"tag_name":"v0.2.1"}]`)})
	var out bytes.Buffer
	Notify(dir, current, &out, launch)
	if !strings.Contains(out.String(), "0.2.1 is available") {
		t.Fatalf("next notice = %q", out.String())
	}
}

func TestDailyRefreshAcrossMidnight(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	yesterday := now.Add(-24 * time.Hour)
	path := filepath.Join(dir, "check-"+yesterday.Format(time.DateOnly))
	if !claim(path) {
		t.Fatal("could not create yesterday's marker")
	}
	recent := yesterday.Add(time.Millisecond)
	if err := os.Chtimes(path, recent, recent); err != nil {
		t.Fatal(err)
	}
	if claimDailyCheck(dir, now) {
		t.Fatal("allowed a check within 24 hours")
	}
	if err := os.Chtimes(path, yesterday, yesterday); err != nil {
		t.Fatal(err)
	}
	if !claimDailyCheck(dir, now) || claimDailyCheck(dir, now) {
		t.Fatal("expected one new claim after 24 hours")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("old claim was not pruned")
	}
	if claimDailyCheck(dir, yesterday) {
		t.Fatal("allowed an older command to claim after the next day's check")
	}
}

func TestCacheRejectsInvalidCandidatesAndSeparatesChannels(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	var calls int
	launch := func() error { calls++; return errors.New("spawn failed") }
	for _, current := range []string{"dev", "1.0.0", "1.0.0+dev-host", "1.0.0-beta.1", "1.0.0-beta.1"} {
		Notify(dir, current, &out, launch)
	}
	if calls != 2 {
		t.Fatalf("launched %d checks across two channels", calls)
	}
	for _, candidate := range []string{"v3.0.0-beta.1", "1.0.0", "0.4.0", "../../3.0.0", "3.0.0\nInjected notice", "3.0.0+build", "vv3.0.0"} {
		if err := os.WriteFile(filepath.Join(dir, "stable", "latest"), []byte(candidate), 0600); err != nil {
			t.Fatal(err)
		}
		Notify(dir, "1.0.0", &out, launch)
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected notice %q", out.String())
	}
}
