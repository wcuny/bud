//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// Puts the child in its own session 
// so it is not tied to this terminal
func detachAttrs(c *exec.Cmd) error {
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}