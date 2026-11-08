//go:build linux || darwin || freebsd

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

// injectInput pushes text into the controlling terminal's input queue
// (TIOCSTI), so the shell shows it at the next prompt as if it had been typed.
// Echo is disabled meanwhile so the text is not printed twice.
func injectInput(text string) error {
	fd, err := syscall.Open("/dev/tty", syscall.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("no terminal: %w", err)
	}
	tty := os.NewFile(uintptr(fd), "/dev/tty")
	defer tty.Close()

	c := exec.Command("stty", "-g")
	c.Stdin = tty
	out, err := c.Output()
	if err != nil {
		return fmt.Errorf("stty: %w", err)
	}
	saved := strings.TrimSpace(string(out))
	if err := sttyRun(tty, "-echo"); err != nil {
		return fmt.Errorf("stty: %w", err)
	}
	defer sttyRun(tty, saved)

	for i := 0; i < len(text); i++ {
		b := text[i]
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, tty.Fd(), uintptr(syscall.TIOCSTI), uintptr(unsafe.Pointer(&b))); errno != 0 {
			return fmt.Errorf("terminal refused input injection: %w", errno)
		}
	}
	return nil
}
