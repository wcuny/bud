//go:build !(linux || darwin || freebsd)

package main

import "errors"

func injectInput(string) error {
	return errors.New("not supported on this platform")
}
