package main

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

func copyToClipboard(text string) error {
	var tools [][]string
	switch runtime.GOOS {
	case "darwin":
		tools = [][]string{{"pbcopy"}}
	case "windows":
		tools = [][]string{{"clip"}}
	default:
		tools = [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
	}
	for _, t := range tools {
		path, err := exec.LookPath(t[0])
		if err != nil {
			continue
		}
		c := exec.Command(path, t[1:]...)
		c.Stdin = strings.NewReader(text)
		if c.Run() == nil {
			return nil
		}
	}
	return errors.New("no working clipboard tool found (install wl-clipboard, xclip, or xsel)")
}
