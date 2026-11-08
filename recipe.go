package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const recipeFileName = "bud.yaml"

type recipeFile struct {
	Commands []recipe `yaml:"commands"`
}

type recipe struct {
	Name    string   `yaml:"name"`
	Command string   `yaml:"command"`
	Tip     string   `yaml:"tip,omitempty"`
	Tags    []string `yaml:"tags,omitempty,flow"`
	// "global", or omitted: omitted means the importer's current scope.
	Scope string `yaml:"scope,omitempty"`
}

// ---- export ----

func newExportCmd() *cobra.Command {
	var global, withGlobal, all, force bool
	var output string
	c := &cobra.Command{
		Use:   "export [--with-global | --global | --all] [-o <file>] [-f]",
		Short: "Write commands to a bud.yaml file",
		Long: `Write commands to a bud.yaml file (default: bud.yaml in the current
directory/repo root).

Which commands are exported:
  (default)       this directory/repo only
  --with-global   this directory/repo + global
  --global        global only
  --all           every scope + global

Directory paths differ between machines, so scope is exported as "global" for
global commands and left blank for everything else. 'bud sync' saves blank scope
commands into the importer's current directory/repo.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			n := 0
			for _, b := range []bool{global, withGlobal, all} {
				if b {
					n++
				}
			}
			if n > 1 {
				return errors.New("--global, --with-global and --all are mutually exclusive")
			}
			st, err := openStore()
			if err != nil {
				return fail(err)
			}
			defer st.Close()
			cmds, err := st.List()
			if err != nil {
				return fail(err)
			}

			scope := currentScope()
			var picked []Command
			for _, c := range cmds {
				switch {
				case all,
					global && c.Scope == "",
					withGlobal && (c.Scope == scope || c.Scope == ""),
					!global && !withGlobal && c.Scope == scope:
					picked = append(picked, c)
				}
			}
			if len(picked) == 0 {
				return fail(errors.New("no commands to export in that selection (try --with-global or --all)"))
			}
			data, err := marshalRecipes(picked)
			if err != nil {
				return fail(err)
			}

			if output == "-" {
				_, err := os.Stdout.Write(data)
				if err != nil {
					return fail(err)
				}
				return nil
			}
			if output == "" {
				output = filepath.Join(scope, recipeFileName)
			}
			if _, err := os.Stat(output); err == nil && !force {
				if !isTerminal(os.Stdin) {
					return fail(fmt.Errorf("%s exists; use --force to overwrite", output))
				}
				fmt.Fprintf(os.Stderr, "%s already exists. Overwrite? [y/N] ", output)
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
					fmt.Fprintln(os.Stderr, "bud: cancelled")
					return exitError{1}
				}
			}
			if err := os.WriteFile(output, data, 0o644); err != nil {
				return fail(err)
			}
			fmt.Fprintf(os.Stderr, "bud: exported %d command(s) to %s\n", len(picked), output)
			return nil
		},
	}
	f := c.Flags()
	f.BoolVar(&withGlobal, "with-global", false, "export this scope + global")
	f.BoolVar(&global, "global", false, "export global commands only")
	f.BoolVar(&all, "all", false, "export every scope + global")
	f.StringVarP(&output, "output", "o", "", "output file (default: bud.yaml in the current directory/repo; - for stdout)")
	f.BoolVarP(&force, "force", "f", false, "overwrite an existing file without asking")
	return c
}

func marshalRecipes(cmds []Command) ([]byte, error) {
	seen := map[string]Command{}
	var rs []recipe
	for _, c := range cmds {
		r := recipe{Name: c.Name, Command: c.Cmd, Tip: c.Tip, Tags: c.Tags}
		if c.Scope == "" {
			r.Scope = "global"
		}
		// Non-global scopes all export as blank, so two entries with the same
		// name would collide on import.
		key := r.Name + "\x00" + r.Scope
		if prev, dup := seen[key]; dup {
			return nil, fmt.Errorf("%q exists in more than one scope that exports as the same scope (%s and %s); narrow the selection",
				c.Name, scopeLabel(prev.Scope), scopeLabel(c.Scope))
		}
		seen[key] = c
		rs = append(rs, r)
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Name != rs[j].Name {
			return rs[i].Name < rs[j].Name
		}
		return rs[i].Scope < rs[j].Scope
	})

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(recipeFile{Commands: rs}); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---- sync ----

func newSyncCmd() *cobra.Command {
	var overwrite bool
	c := &cobra.Command{
		Use:   "sync [file] [--overwrite]",
		Short: "Import commands from a bud.yaml file",
		Long: `Import commands from a bud.yaml file (default: bud.yaml in the current
directory/repo root) into your local library.

Commands with a blank scope are saved to the current directory/repo; commands
with scope "global" are saved globally.

When a command with the same name and scope already exists and differs,
bud shows the differences and asks before replacing it.
Use --overwrite to replace without asking. Identical commands are left
alone, and local commands that are not in the file are never removed.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope := currentScope()
			path := filepath.Join(scope, recipeFileName)
			if len(args) == 1 {
				path = args[0]
			}
			cmds, err := loadRecipes(path, scope)
			if err != nil {
				return fail(err)
			}
			st, err := openStore()
			if err != nil {
				return fail(err)
			}
			defer st.Close()

			in := bufio.NewReader(os.Stdin)
			interactive := isTerminal(os.Stdin)
			color := isTerminal(os.Stderr) && os.Getenv("NO_COLOR") == ""
			var added, replaced, unchanged, skipped, unresolved int

			for i := range cmds {
				c := &cmds[i]
				existing, err := st.byName(c.Scope, c.Name)
				if errors.Is(err, sql.ErrNoRows) {
					if _, err := st.Upsert(c); err != nil {
						return fail(err)
					}
					added++
					continue
				}
				if err != nil {
					return fail(err)
				}
				if sameRecipe(existing, c) {
					unchanged++
					continue
				}
				if !overwrite {
					if !interactive {
						fmt.Fprintf(os.Stderr, "bud: skipped %q (%s): differs from the local command; use --overwrite\n", c.Name, scopeLabel(c.Scope))
						skipped++
						unresolved++
						continue
					}
					printConflict(os.Stderr, existing, c, color)
					fmt.Fprintf(os.Stderr, "Overwrite %q with the version from %s? [y/N] ", c.Name, filepath.Base(path))
					line, _ := in.ReadString('\n')
					if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
						skipped++
						continue
					}
				}
				if _, err := st.Upsert(c); err != nil {
					return fail(err)
				}
				replaced++
			}
			fmt.Fprintf(os.Stderr, "bud: synced %s: %d added, %d overwritten, %d unchanged, %d skipped\n",
				path, added, replaced, unchanged, skipped)
			if unresolved > 0 {
				return exitError{1}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&overwrite, "overwrite", false, "overwrite differing commands without asking")
	return c
}

// loadRecipes reads and fully validates a bud.yaml before anything is
// written, resolving each entry's scope for this machine.
func loadRecipes(path, scope string) ([]Command, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s not found (pass a file path to sync another file)", path)
	}
	if err != nil {
		return nil, err
	}
	var f recipeFile
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true) // catch typos like "cmd:" instead of "command:"
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s is empty", path)
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(f.Commands) == 0 {
		return nil, fmt.Errorf("%s has no commands", path)
	}

	seen := map[string]bool{}
	out := make([]Command, 0, len(f.Commands))
	for i, r := range f.Commands {
		name := strings.TrimSpace(r.Name)
		where := fmt.Sprintf("%s: entry %d", path, i+1)
		if name == "" {
			return nil, fmt.Errorf("%s: name is required", where)
		}
		if strings.TrimSpace(r.Command) == "" {
			return nil, fmt.Errorf("%s (%q): command is required", where, name)
		}
		c := Command{Name: name, Cmd: r.Command, Tip: r.Tip, Tags: normalizeTags(r.Tags)}
		switch strings.ToLower(strings.TrimSpace(r.Scope)) {
		case "":
			c.Scope = scope
		case "global":
			c.Scope = ""
		default:
			return nil, fmt.Errorf("%s (%q): scope %q is not supported (use \"global\" or leave it blank)", where, name, r.Scope)
		}
		key := c.Name + "\x00" + c.Scope
		if seen[key] {
			return nil, fmt.Errorf("%s (%q): duplicate name with the same scope", where, name)
		}
		seen[key] = true
		out = append(out, c)
	}
	return out, nil
}

func sameRecipe(a, b *Command) bool {
	ta, tb := slices.Clone(a.Tags), slices.Clone(b.Tags)
	slices.Sort(ta)
	slices.Sort(tb)
	return a.Cmd == b.Cmd && a.Tip == b.Tip && slices.Equal(ta, tb)
}

// printConflict shows how the local command and the file's version differ.
func printConflict(w io.Writer, local, file *Command, color bool) {
	paint := func(code, s string) string {
		if color {
			return "\x1b[" + code + "m" + s + "\x1b[0m"
		}
		return s
	}
	lines := func(s string) []string {
		if s == "" {
			return []string{"(none)"}
		}
		return strings.Split(s, "\n")
	}
	field := func(label, l, f string, same bool) {
		if same {
			ls := lines(l)
			if len(ls) == 1 {
				fmt.Fprintf(w, "  %s: %s\n", label, ls[0])
				return
			}
			fmt.Fprintf(w, "  %s:\n", label)
			for _, ln := range ls {
				fmt.Fprintf(w, "      %s\n", ln)
			}
			return
		}
		fmt.Fprintf(w, "  %s:\n", label)
		for _, ln := range lines(l) {
			fmt.Fprintln(w, paint("31", "    - "+ln))
		}
		for _, ln := range lines(f) {
			fmt.Fprintln(w, paint("32", "    + "+ln))
		}
	}

	fmt.Fprintf(w, "\n%s [%s] differs from the file  (%s local, %s file)\n", local.Name, scopeLabel(local.Scope),
		paint("31", "-"), paint("32", "+"))
	field("command", local.Cmd, file.Cmd, local.Cmd == file.Cmd)
	field("tip", local.Tip, file.Tip, local.Tip == file.Tip)
	lt, ft := strings.Join(local.Tags, ", "), strings.Join(file.Tags, ", ")
	tagsSame := slices.Equal(sortedCopy(local.Tags), sortedCopy(file.Tags))
	field("tags", lt, ft, tagsSame)
}

func sortedCopy(s []string) []string {
	c := slices.Clone(s)
	slices.Sort(c)
	return c
}