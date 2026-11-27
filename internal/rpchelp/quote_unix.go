//go:build !windows

package rpchelp

func shellExamples(command, body, profileFlag string) string {
	return posixExample(command, body, profileFlag)
}
