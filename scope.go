package main

import (
	"os"
	"path/filepath"
)

// currentScope returns the git repo root containing the working directory,
// or the working directory itself when not inside a repo. "" means global
// and is never returned here.
func currentScope() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(wd); err == nil {
		wd = real
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return wd
		}
		dir = parent
	}
}
