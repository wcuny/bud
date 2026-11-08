package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf8"
)

const (
	keyNone = iota
	keyRune
	keyEnter
	keyCancel
	keyUp
	keyDown
	keyBackspace
	keyClear
	keyTab
)

const (
	modeHere   = iota // this scope only
	modeBoth          // this scope + global
	modeGlobal        // global only
	modeAll           // every scope + global
	modeCount
)

var modeLabels = [modeCount]string{"this scope only", "this scope + global", "global only", "all scopes"}

func sttyRun(tty *os.File, args ...string) error {
	c := exec.Command("stty", args...)
	c.Stdin = tty
	return c.Run()
}

func termSize(tty *os.File) (rows, cols int) {
	rows, cols = 24, 80
	c := exec.Command("stty", "size")
	c.Stdin = tty
	if out, err := c.Output(); err == nil {
		var r, cl int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d %d", &r, &cl); err == nil && r > 0 && cl > 0 {
			rows, cols = r, cl
		}
	}
	return
}

func readByte(tty *os.File) (byte, bool, error) {
	var b [1]byte
	n, err := tty.Read(b[:])
	if n == 1 {
		return b[0], true, nil
	}
	if err != nil && !errors.Is(err, io.EOF) { // EOF == VTIME timeout
		return 0, false, err
	}
	return 0, false, nil
}

func readKey(tty *os.File) (int, rune) {
	b, ok, err := readByte(tty)
	if err != nil {
		return keyCancel, 0
	}
	if !ok {
		return keyNone, 0
	}
	switch {
	case b == 27:
		b2, ok, _ := readByte(tty)
		if !ok {
			return keyCancel, 0 // lone Esc
		}
		if b2 != '[' && b2 != 'O' {
			return keyNone, 0
		}
		for {
			b3, ok, _ := readByte(tty)
			if !ok {
				return keyNone, 0
			}
			switch {
			case b3 == 'A':
				return keyUp, 0
			case b3 == 'B':
				return keyDown, 0
			case (b3 >= 'A' && b3 <= 'Z') || (b3 >= 'a' && b3 <= 'z') || b3 == '~':
				return keyNone, 0
			}
		}
	case b == '\r' || b == '\n':
		return keyEnter, 0
	case b == 3 || b == 7 || b == 4: // ^C ^G ^D
		return keyCancel, 0
	case b == 16: // ^P
		return keyUp, 0
	case b == 14: // ^N
		return keyDown, 0
	case b == 127 || b == 8:
		return keyBackspace, 0
	case b == 21: // ^U
		return keyClear, 0
	case b == 9:
		return keyTab, 0
	case b < 32:
		return keyNone, 0
	}
	buf := []byte{b}
	if b >= 0xC0 {
		need := 1
		if b >= 0xF0 {
			need = 3
		} else if b >= 0xE0 {
			need = 2
		}
		for i := 0; i < need; i++ {
			nb, ok, _ := readByte(tty)
			if !ok {
				break
			}
			buf = append(buf, nb)
		}
	}
	r, _ := utf8.DecodeRune(buf)
	if r == utf8.RuneError {
		return keyNone, 0
	}
	return keyRune, r
}

func padRight(s string, w int) string {
	if n := utf8.RuneCountInString(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func render(tty *os.File, items []Command, sel int, q string, mode int) {
	rows, cols := termSize(tty)
	width := cols - 1 // avoid terminal autowrap on the last column

	// Column widths: NAME | TAGS | COMMAND (command takes the remaining width).
	w1, w2 := 4, 4
	for _, c := range items {
		w1 = max(w1, utf8.RuneCountInString(c.Name))
		w2 = max(w2, utf8.RuneCountInString(strings.Join(c.Tags, ", ")))
	}
	w1, w2 = min(w1, 24), min(w2, 24)
	w3 := max(10, width-8-w1-w2)

	const dim, bold, rev, reset = "\x1b[2m", "\x1b[1m", "\x1b[7m", "\x1b[0m"
	var b strings.Builder
	b.WriteString("\x1b[H\x1b[2J")

	// Top: search + (match count, scope filter).
	fmt.Fprintf(&b, "%s> %s%s\r\n", bold, reset, q)
	noun := "matches"
	if len(items) == 1 {
		noun = "match"
	}
	fmt.Fprintf(&b, "%d %s %s·%s %s%s%s\r\n", len(items), noun, dim, reset, bold, modeLabels[mode], reset)

	// Table header + rule.
	fmt.Fprintf(&b, "%s  %s │ %s │ %s%s\r\n", bold, padRight("NAME", w1), padRight("TAGS", w2), "COMMAND", reset)
	fmt.Fprintf(&b, "%s──%s─┼─%s─┼─%s%s\r\n", dim, strings.Repeat("─", w1), strings.Repeat("─", w2), strings.Repeat("─", w3), reset)

	height := max(1, rows-8)
	if len(items) == 0 {
		fmt.Fprintf(&b, "%s  (no matches)%s\r\n", dim, reset)
	}
	offset := max(0, sel-height+1)
	for i := offset; i < len(items) && i < offset+height; i++ {
		c := items[i]
		name := padRight(trunc(c.Name, w1), w1)
		tags := padRight(trunc(strings.Join(c.Tags, ", "), w2), w2)
		cmd := trunc(oneLine(c.Cmd), w3)
		if i == sel {
			fmt.Fprintf(&b, "%s> %s │ %s │ %s%s\r\n", rev, name, tags, padRight(cmd, w3), reset)
		} else {
			sep := dim + " │ " + reset
			fmt.Fprintf(&b, "  %s%s%s%s%s\r\n", name, sep, tags, sep, cmd)
		}
	}

	// Bottom: separator, selected command + tip/scope, controls.
	fmt.Fprintf(&b, "\x1b[%d;1H%s%s%s", rows-3, dim, strings.Repeat("─", width), reset)
	if len(items) > 0 {
		c := items[sel]
		fmt.Fprintf(&b, "\x1b[%d;1H%s$ %s%s", rows-2, dim, trunc(oneLine(c.Cmd), width-2), reset)
		info := scopeLabel(c.Scope)
		if c.Tip != "" {
			info = c.Tip + " · " + info
		}
		fmt.Fprintf(&b, "\x1b[%d;1H%s%s%s", rows-1, dim, trunc(oneLine(info), width), reset)
	}
	fmt.Fprintf(&b, "\x1b[%d;1H%s↑/↓ move · enter select · tab scope filter · esc cancel%s", rows, dim, reset)
	tty.WriteString(b.String())
}

// pick shows the picker and returns the chosen command, or nil if cancelled.
func pick(cmds []Command, scope string) (*Command, error) {
	fd, err := syscall.Open("/dev/tty", syscall.O_RDWR, 0)
	if err != nil {
		return nil, errors.New("no terminal available; use `bud run <name>`")
	}
	// NewFile on a blocking fd keeps reads blocking, so VMIN/VTIME timeouts work.
	tty := os.NewFile(uintptr(fd), "/dev/tty")
	defer tty.Close()

	c := exec.Command("stty", "-g")
	c.Stdin = tty
	out, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("stty: %w", err)
	}
	saved := strings.TrimSpace(string(out))
	if err := sttyRun(tty, "-icanon", "-echo", "-isig", "-ixon", "min", "0", "time", "1"); err != nil {
		return nil, fmt.Errorf("stty: %w", err)
	}
	defer sttyRun(tty, saved)
	tty.WriteString("\x1b[?1049h\x1b[?25l")
	defer tty.WriteString("\x1b[?25h\x1b[?1049l")

	var query []rune
	sel, mode, dirty := 0, modeBoth, true
	visible := func() []Command {
		var src []Command
		for _, c := range cmds {
			ok := false
			switch mode {
			case modeBoth:
				ok = c.Scope == scope || c.Scope == ""
			case modeGlobal:
				ok = c.Scope == ""
			case modeHere:
				ok = c.Scope == scope
			case modeAll:
				ok = true
			}
			if ok {
				src = append(src, c)
			}
		}
		return rank(src, string(query), scope)
	}
	items := visible()
	for {
		if sel >= len(items) {
			sel = len(items) - 1
		}
		if sel < 0 {
			sel = 0
		}
		if dirty {
			render(tty, items, sel, string(query), mode)
			dirty = false
		}
		k, r := readKey(tty)
		switch k {
		case keyNone:
			continue
		case keyEnter:
			if len(items) == 0 {
				continue
			}
			chosen := items[sel]
			return &chosen, nil
		case keyCancel:
			return nil, nil
		case keyUp:
			sel--
		case keyDown:
			sel++
		case keyBackspace:
			if len(query) > 0 {
				query = query[:len(query)-1]
			}
			sel = 0
		case keyClear:
			query, sel = nil, 0
		case keyTab:
			mode, sel = (mode+1)%modeCount, 0
		case keyRune:
			query = append(query, r)
			sel = 0
		}
		items = visible()
		dirty = true
	}
}

// ---- handing the chosen command back to the shell prompt ----

const maxInject = 1000

// deliver puts cmd in the user's terminal input so it can be edited before
// running. When stdout is not a terminal (something like: print -z "$(bud)") it is just
// printed. If it cannot be inserted safely, it is printed instead.
func deliver(cmd string) {
	if !isTerminal(os.Stdout) {
		fmt.Println(cmd)
		return
	}
	if why := notInsertable(cmd); why != "" {
		printInstead(cmd, why)
		return
	}
	if err := injectInput(cmd); err != nil {
		printInstead(cmd, err.Error())
	}
}

func printInstead(cmd, why string) {
	fmt.Fprintf(os.Stderr, "bud: could not put the command in your prompt (%s); printing it instead\n", why)
	fmt.Println(cmd)
}

// notInsertable reports why cmd must not be typed into the terminal:
// control characters (newlines, tabs, escapes) would be interpreted by the
// shell or the tty instead of being inserted literally.
func notInsertable(cmd string) string {
	if len(cmd) > maxInject {
		return "command is too long"
	}
	for i := 0; i < len(cmd); i++ {
		if b := cmd[i]; b < 0x20 || b == 0x7f {
			return "it contains newlines or control characters"
		}
	}
	return ""
}
