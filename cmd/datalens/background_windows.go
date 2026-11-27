package main

import (
	"syscall"

	"golang.org/x/sys/windows"
)

func backgroundProcessAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}
