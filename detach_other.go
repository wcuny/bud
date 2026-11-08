//go:build !unix

package main

import (
	"errors"
	"os/exec"
)

func detachAttrs(*exec.Cmd) error {
	return errors.New("--detach is not supported on this platform")
}