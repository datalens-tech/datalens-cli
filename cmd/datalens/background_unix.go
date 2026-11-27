//go:build unix

package main

import "syscall"

func backgroundProcessAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
