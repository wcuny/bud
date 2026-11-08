package main

import (
	"os"
	"os/user"
	"regexp"
	"strings"
	"time"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// shQuote single quotes s for POSIX shells.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", " ⏎ ")
}

func trunc(s string, n int) string {
	r := []rune(s)
	if n <= 0 {
		return ""
	}
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// slugify derives a fallback name from a command string.
func slugify(cmd string) string {
	words := strings.Fields(strings.ToLower(cmd))
	if len(words) > 3 {
		words = words[:3]
	}
	s := strings.Trim(nonAlnum.ReplaceAllString(strings.Join(words, "-"), "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		s = "cmd"
	}
	return s
}

func normalizeTags(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range in {
		for _, p := range strings.Split(t, ",") {
			p = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(p)), " ", "-")
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func hasTag(c Command, tag string) bool {
	for _, t := range c.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func scopeLabel(scope string) string {
	if scope == "" {
		return "global"
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" && strings.HasPrefix(scope, u.HomeDir) {
		return "~" + strings.TrimPrefix(scope, u.HomeDir)
	}
	return scope
}

func fmtTime(unix int64) string {
	if unix == 0 {
		return "never"
	}
	return time.Unix(unix, 0).Format("2006-01-02 15:04")
}
