package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
)

func shellInvocation() (string, string) {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh, "-c"
	}
	if runtime.GOOS == "windows" {
		return "cmd", "/C"
	}
	return "/bin/sh", "-c"
}

// execShell runs target via $SHELL -c with the standard streams attached
// directly, and returns the exit code.
func execShell(target string) int {
	sh, flag := shellInvocation()
	cmd := exec.Command(sh, flag, target)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	// Let Ctrl-C reach the child (same process group) without killing bud first.
	// Notify (not Ignore) so the child does not inherit an ignored SIGINT.
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	go func() {
		for range sig {
		}
	}()

	err := cmd.Run()
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	fmt.Fprintf(os.Stderr, "bud: cannot start %s: %v\n", sh, err)
	return 127
}
