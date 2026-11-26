package rpchelp

import (
	"strings"

	"golang.org/x/sys/windows"
)

func shellExamples(command, body, profileFlag string) string {
	// PowerShell still expands %NAME% after its stop-parsing token.
	argument := windows.EscapeArg(strings.ReplaceAll(body, "%", `\u0025`))
	if !strings.HasPrefix(argument, `"`) {
		argument = `"` + argument + `"`
	}
	powershell := command + " --% " + argument + profileFlag
	return "PowerShell:\n    " + powershell + "\n  Git Bash:\n    " + posixExample(command, body, profileFlag)
}
