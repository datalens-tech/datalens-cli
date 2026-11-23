package buildinfo

import (
	"runtime"
	"runtime/debug"
)

var Version = "dev"
var Revision = "unknown"

type Info struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GoVersion string `json:"goVersion"`
}

func Current() Info {
	revision := Revision
	if revision == "unknown" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					revision = setting.Value
				}
			}
		}
	}
	return Info{Version: Version, Revision: revision, OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version()}
}
