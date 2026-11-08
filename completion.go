package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	rcBegin = "# >>> bud completion >>>"
	rcEnd   = "# <<< bud completion <<<"
)

// completeNames completes the <name> argument from the library (current
// scope first, then global: the same lookup run/info/edit/rm/copy use).
func completeNames(filesAfterName bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			if filesAfterName {
				return nil, cobra.ShellCompDirectiveDefault
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		st, err := openStore()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		defer st.Close()
		all, err := st.List()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		scope := currentScope()
		seen := map[string]bool{}
		var out []string
		for _, c := range rank(all, "", scope) {
			if (c.Scope != scope && c.Scope != "") || seen[c.Name] {
				continue
			}
			seen[c.Name] = true
			if !strings.HasPrefix(c.Name, toComplete) {
				continue
			}
			entry := c.Name
			if tip := strings.ReplaceAll(oneLine(c.Tip), "\t", " "); tip != "" {
				entry += "\t" + trunc(tip, 60)
			}
			out = append(out, entry)
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

func newCompletionCmd(root *cobra.Command) *cobra.Command {
	c := &cobra.Command{
		Use:   "completion",
		Short: "Generate or install shell completion (bash, zsh, fish)",
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		shell := shell
		c.AddCommand(&cobra.Command{
			Use:   shell,
			Short: "Print the " + shell + " completion script",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return genScript(root, shell, cmd.OutOrStdout())
			},
		})
	}
	c.AddCommand(
		&cobra.Command{
			Use:   "install",
			Short: "Install completion for your shell into a user level directory",
			Long: `Detects your shell from $SHELL and installs the completion script into a
user level directory. Shell config files are only touched when zsh has no
writable directory in its fpath (see the message printed).`,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := installCompletion(root); err != nil {
					return fail(err)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "uninstall",
			Short: "Remove the user level completion files installed by 'bud completion install'",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := uninstallCompletion(); err != nil {
					return fail(err)
				}
				return nil
			},
		},
	)
	return c
}

func genScript(root *cobra.Command, shell string, w io.Writer) error {
	switch shell {
	case "bash":
		return root.GenBashCompletionV2(w, true)
	case "zsh":
		return root.GenZshCompletion(w)
	case "fish":
		return root.GenFishCompletion(w, true)
	}
	return fmt.Errorf("unsupported shell %q (bash, zsh, fish)", shell)
}

func detectShell() (string, error) {
	switch sh := filepath.Base(os.Getenv("SHELL")); sh {
	case "bash", "zsh", "fish":
		return sh, nil
	}
	return "", fmt.Errorf("cannot detect a supported shell from $SHELL=%q (supported: bash, zsh, fish)", os.Getenv("SHELL"))
}

func xdgDir(env string, fallback ...string) (string, error) {
	if v := os.Getenv(env); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, fallback...)...), nil
}

func bashPath() (string, error) {
	d, err := xdgDir("XDG_DATA_HOME", ".local", "share")
	return filepath.Join(d, "bash-completion", "completions", "bud"), err
}

func fishPath() (string, error) {
	d, err := xdgDir("XDG_CONFIG_HOME", ".config")
	return filepath.Join(d, "fish", "completions", "bud.fish"), err
}

func zshFallbackDir() (string, error) {
	d, err := xdgDir("XDG_DATA_HOME", ".local", "share")
	return filepath.Join(d, "zsh", "site-functions"), err
}

func zshrcPath() (string, error) {
	if z := os.Getenv("ZDOTDIR"); z != "" {
		return filepath.Join(z, ".zshrc"), nil
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".zshrc"), err
}

// zshFpath asks the user's own (interactive) zsh for its fpath.
func zshFpath() []string {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, zsh, "-ic", "print -r -- __BUD_FPATH__; print -rl -- $fpath").Output()
	_, rest, ok := strings.Cut(string(out), "__BUD_FPATH__\n")
	if !ok {
		return nil
	}
	var dirs []string
	for _, l := range strings.Split(rest, "\n") {
		if l = strings.TrimSpace(l); filepath.IsAbs(l) {
			dirs = append(dirs, l)
		}
	}
	return dirs
}

func dirWritable(d string) bool {
	f, err := os.CreateTemp(d, ".bud-write-test-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

func inDir(path, dir string) bool {
	return dir != "" && strings.HasPrefix(path, dir+string(os.PathSeparator))
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func installCompletion(root *cobra.Command) error {
	shell, err := detectShell()
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := genScript(root, shell, &buf); err != nil {
		return err
	}
	switch shell {
	case "bash":
		p, err := bashPath()
		if err != nil {
			return err
		}
		if err := writeFile(p, buf.Bytes()); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "bud: installed bash completion to %s\nbud: it is loaded automatically in new terminals if bash-completion (v2) is installed\n", p)
	case "fish":
		p, err := fishPath()
		if err != nil {
			return err
		}
		if err := writeFile(p, buf.Bytes()); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "bud: installed fish completion to %s (open a new terminal)\n", p)
	case "zsh":
		return installZsh(buf.Bytes())
	}
	return nil
}

func installZsh(script []byte) error {
	fpath := zshFpath()
	home, _ := os.UserHomeDir()
	fallback, err := zshFallbackDir()
	if err != nil {
		return err
	}

	dir, needRC := "", false
	for _, d := range fpath {
		if inDir(d, home) && dirWritable(d) {
			dir = d
			break
		}
	}
	if dir == "" {
		dir = fallback
		needRC = true
		for _, d := range fpath {
			if d == fallback {
				needRC = false // already on the user's fpath
			}
		}
	}
	p := filepath.Join(dir, "_bud")
	if err := writeFile(p, script); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "bud: installed zsh completion to %s\n", p)
	if !needRC {
		fmt.Fprintln(os.Stderr, "bud: open a new terminal (or run `exec zsh`) to use it")
		return nil
	}

	rc, err := zshrcPath()
	if err != nil {
		return err
	}
	cur, err := os.ReadFile(rc)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if bytes.Contains(cur, []byte(rcBegin)) {
		fmt.Fprintf(os.Stderr, "bud: %s already has the bud completion block\n", rc)
		return nil
	}
	block := rcBegin + "\nfpath=(" + shQuote(dir) + " $fpath)\nautoload -Uz compinit && compinit\n" + rcEnd + "\n"
	if len(cur) > 0 && !bytes.HasSuffix(cur, []byte("\n")) {
		block = "\n" + block
	}
	f, err := os.OpenFile(rc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(block); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "bud: no writable directory in your zsh fpath, so a marked block was added to %s\n", rc)
	fmt.Fprintln(os.Stderr, "bud: open a new terminal (or run `exec zsh`) to use it; `bud completion uninstall` removes it")
	return nil
}

// isOurs avoids deleting an unrelated file that happens to share the name.
func isOurs(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, "#compdef bud") ||
		strings.Contains(s, "completion V2 for bud") ||
		strings.Contains(s, "fish completion for bud")
}

func uninstallCompletion() error {
	var candidates []string
	if p, err := bashPath(); err == nil {
		candidates = append(candidates, p)
	}
	if p, err := fishPath(); err == nil {
		candidates = append(candidates, p)
	}
	home, _ := os.UserHomeDir()
	if d, err := zshFallbackDir(); err == nil {
		candidates = append(candidates, filepath.Join(d, "_bud"))
	}
	for _, d := range zshFpath() {
		if inDir(d, home) {
			candidates = append(candidates, filepath.Join(d, "_bud"))
		}
	}

	removed := false
	seen := map[string]bool{}
	for _, p := range candidates {
		if seen[p] {
			continue
		}
		seen[p] = true
		if !isOurs(p) {
			continue
		}
		if err := os.Remove(p); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "bud: removed %s\n", p)
		removed = true
	}

	if rc, err := zshrcPath(); err == nil {
		if ok, err := removeRCBlock(rc); err != nil {
			return err
		} else if ok {
			fmt.Fprintf(os.Stderr, "bud: removed the bud completion block from %s\n", rc)
			removed = true
		}
	}
	if !removed {
		fmt.Fprintln(os.Stderr, "bud: nothing to remove")
	}
	return nil
}

func removeRCBlock(rc string) (bool, error) {
	b, err := os.ReadFile(rc)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s := string(b)
	i := strings.Index(s, rcBegin)
	j := strings.Index(s, rcEnd)
	if i < 0 || j < i {
		return false, nil
	}
	j += len(rcEnd)
	if j < len(s) && s[j] == '\n' {
		j++
	}
	out := s[:i] + s[j:]
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(rc); err == nil {
		mode = fi.Mode().Perm()
	}
	return true, os.WriteFile(rc, []byte(out), mode)
}
