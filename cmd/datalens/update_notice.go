package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/datalens-tech/datalens-cli/internal/buildinfo"
	"github.com/datalens-tech/datalens-cli/internal/updatenotice"
)

const refreshCommand = "__refresh-update-notice"

func noticeDir() string {
	dir, err := os.UserCacheDir()
	if err != nil || !filepath.IsAbs(dir) {
		return ""
	}
	return filepath.Join(dir, "datalens", "updates")
}

func refreshUpdateNotice() {
	dir := noticeDir()
	if dir == "" || os.Getenv("DATALENS_NO_UPDATE_CHECK") == "1" {
		return
	}
	updatenotice.Refresh(context.Background(), dir, buildinfo.Version, http.DefaultClient)
}

func notifyUpdate(current string, out io.Writer) {
	if dir := noticeDir(); dir != "" {
		updatenotice.Notify(dir, current, out, launchRefresh)
	}
}

func launchRefresh() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(executable, refreshCommand)
	cmd.SysProcAttr = backgroundProcessAttrs()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
