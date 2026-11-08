package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

const version = "0.1.0"

// exitError carries a process exit code out of a command (the message, if
// any, has already been printed).
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

func exitWith(code int) error {
	if code == 0 {
		return nil
	}
	return exitError{code}
}

// fail prints err and returns an exit-1 error.
func fail(err error) error {
	fmt.Fprintln(os.Stderr, "bud:", err)
	return exitError{1}
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		var ee exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
		// Usage errors (unknown command, bad flags or arguments).
		fmt.Fprintln(os.Stderr, "bud:", err)
		os.Exit(2)
	}
}

func newRootCmd() *cobra.Command {
	cobra.EnableCommandSorting = false
	root := &cobra.Command{
		Use:   "bud",
		Short: "Library of commands",
		Long: `bud keeps a library of commands with names, tips, tags and directory/repo scoping.

Run with no arguments to open the interactive picker. The command you select
is placed in your terminal prompt so you can edit it before running.`,
		Version:           version,
		SilenceUsage:      true,
		SilenceErrors:     true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		RunE:              func(cmd *cobra.Command, args []string) error { return runPicker() },
	}
	root.SetVersionTemplate("bud {{.Version}}\n")
	root.AddCommand(
		newDoCmd(), newAddCmd(), newRunCmd(), newListCmd(), newSearchCmd(),
		newInfoCmd(), newEditCmd(), newRmCmd(), newCopyCmd(), newExportCmd(), newSyncCmd(), newUICmd(),
		newCompletionCmd(root), newVersionCmd(),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, args []string) { fmt.Println("bud", version) },
	}
}

// ---- save / do ----

type saveOpts struct {
	cmd, name, tip string
	tags           []string
	global         bool
}

func (o *saveOpts) bind(c *cobra.Command) {
	f := c.Flags()
	f.StringVar(&o.cmd, "cmd", "", "command to save (required)")
	f.StringVar(&o.name, "name", "", "short alias (default: derived from the command)")
	f.StringVar(&o.tip, "tip", "", "tooltip / description")
	f.StringArrayVar(&o.tags, "tag", nil, "tag (repeatable or comma separated)")
	f.BoolVar(&o.global, "global", false, "available in every directory")
	c.MarkFlagRequired("cmd")
}

func (o *saveOpts) command() (*Command, error) {
	if strings.TrimSpace(o.cmd) == "" {
		return nil, errors.New("--cmd is required")
	}
	name := strings.TrimSpace(o.name)
	if name == "" {
		name = slugify(o.cmd)
	}
	c := &Command{Name: name, Cmd: o.cmd, Tip: o.tip, Tags: normalizeTags(o.tags)}
	if !o.global {
		c.Scope = currentScope()
	}
	return c, nil
}

func noExtraArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("unexpected argument %q (quote the --cmd value)", args[0])
	}
	return nil
}

func save(st *Store, c *Command) error {
	created, err := st.Upsert(c)
	if err != nil {
		return err
	}
	verb := "saved"
	if !created {
		verb = "updated"
	}
	fmt.Fprintf(os.Stderr, "bud: %s %q (%s)\n", verb, c.Name, scopeLabel(c.Scope))
	return nil
}

func newAddCmd() *cobra.Command {
	var o saveOpts
	c := &cobra.Command{
		Use:     "add --cmd <command> [--name <alias>] [--tip <text>] [--tag <tag>] [--global]",
		Short:   "Save a command without running it",
		Example: `  bud add --cmd "go test ./..." --name test --tip "Run all tests" --tag go`,
		Args:    noExtraArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := o.command()
			if err != nil {
				return err
			}
			st, err := openStore()
			if err != nil {
				return fail(err)
			}
			defer st.Close()
			if err := save(st, c); err != nil {
				return fail(err)
			}
			return nil
		},
	}
	o.bind(c)
	return c
}

func newDoCmd() *cobra.Command {
	var o saveOpts
	c := &cobra.Command{
		Use:     "do --cmd <command> [--name <alias>] [--tip <text>] [--tag <tag>] [--global]",
		Short:   "Save a command and run it immediately",
		Long:    "Save a command and run it immediately. If the command is not saved, it will be saved to the library.",
		Example: `  bud do --cmd "docker compose up -d" --name up --tip "Start the stack"`,
		Args:    noExtraArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := o.command()
			if err != nil {
				return err
			}
			st, err := openStore()
			if err != nil {
				fmt.Fprintln(os.Stderr, "bud: warning: not saved:", err)
				return exitWith(execShell(c.Cmd)) // still run what the user asked for
			}
			defer st.Close()
			if err := save(st, c); err != nil {
				fmt.Fprintln(os.Stderr, "bud: warning: not saved:", err)
				return exitWith(execShell(c.Cmd))
			}
			code := execShell(c.Cmd)
			st.RecordRun(c.ID)
			return exitWith(code)
		},
	}
	o.bind(c)
	return c
}

// ---- run / picker ----

func newRunCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "run [name] [extra args...]",
		Short: "Run a saved command (no name: interactive picker)",
		Long: `Run a saved command by name. It is looked up in the current directory/repo
scope first, then globally. Extra arguments are shell-quoted and appended.

With no name, opens the interactive picker (same as running bare 'bud').`,
		ValidArgsFunction: completeNames(true),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return runPicker()
			}
			st, err := openStore()
			if err != nil {
				return fail(err)
			}
			defer st.Close()
			c, err := st.Resolve(args[0], currentScope())
			if err != nil {
				return fail(err)
			}
			extra := args[1:]
			if len(extra) > 0 && extra[0] == "--" {
				extra = extra[1:]
			}
			target := c.Cmd
			for _, a := range extra {
				target += " " + shQuote(a)
			}
			code := execShell(target)
			st.RecordRun(c.ID)
			return exitWith(code)
		},
	}
	c.Flags().SetInterspersed(false) // everything after the name is passed through
	return c
}

// runPicker opens the interactive picker and hands the selection back to the
// shell prompt (it does not run it).
func runPicker() error {
	st, err := openStore()
	if err != nil {
		return fail(err)
	}
	defer st.Close()
	all, err := st.List()
	if err != nil {
		return fail(err)
	}
	if len(all) == 0 {
		return fail(errors.New("library is empty; add commands with `bud add` or `bud do`"))
	}
	c, err := pick(all, currentScope())
	if err != nil {
		return fail(err)
	}
	if c == nil {
		return exitWith(130)
	}
	deliver(c.Cmd)
	return nil
}

// ---- list / search ----

func printTable(cmds []Command, showScope bool) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	hdr := "NAME\tTAGS\t"
	if showScope {
		hdr += "SCOPE\t"
	}
	fmt.Fprintln(tw, hdr+"COMMAND\tTIP")
	for _, c := range cmds {
		tags := strings.Join(c.Tags, ",")
		row := fmt.Sprintf("%s\t%s\t", c.Name, tags)
		if showScope {
			row += scopeLabel(c.Scope) + "\t"
		}
		fmt.Fprintln(tw, row+trunc(oneLine(c.Cmd), 60)+"\t"+trunc(oneLine(c.Tip), 50))
	}
	tw.Flush()
}

func newListCmd() *cobra.Command {
	var all, long bool
	var tag string
	c := &cobra.Command{
		Use:     "list [-l] [-a] [--tag <tag>]",
		Aliases: []string{"ls"},
		Short:   "List command names in the current directory/repo",
		Long: `List the names of commands in the current directory/repo.
Use -l for a table with tags, command and tip, and -a to include global and
other directories/repos.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
			tag = strings.ToLower(strings.TrimSpace(tag))
			var keep []Command
			for _, c := range cmds {
				if !all && c.Scope != scope {
					continue
				}
				if tag != "" && !hasTag(c, tag) {
					continue
				}
				keep = append(keep, c)
			}
			if len(keep) == 0 {
				fmt.Fprintln(os.Stderr, "bud: nothing here (try --all)")
				return nil
			}
			keep = rank(keep, "", scope)
			if long {
				printTable(keep, all)
				return nil
			}
			for _, c := range keep {
				fmt.Println(c.Name)
			}
			return nil
		},
	}
	c.Flags().BoolVarP(&long, "long", "l", false, "long format: tags, command and tip in columns")
	c.Flags().BoolVarP(&all, "all", "a", false, "include global and other directories/repos")
	c.Flags().StringVar(&tag, "tag", "", "only this tag")
	return c
}

func newSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "search <query>",
		Short: "Fuzzy search commands, names, tags and tips",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return fail(err)
			}
			defer st.Close()
			cmds, err := st.List()
			if err != nil {
				return fail(err)
			}
			res := rank(cmds, strings.Join(args, " "), currentScope())
			if len(res) == 0 {
				fmt.Fprintln(os.Stderr, "bud: no matches")
				return exitError{1}
			}
			printTable(res, true)
			return nil
		},
	}
}

// ---- info / copy ----

func newInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "info <name>",
		Short:             "Show a command's details without running it",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNames(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return fail(err)
			}
			defer st.Close()
			c, err := st.Resolve(args[0], currentScope())
			if err != nil {
				return fail(err)
			}
			tip := c.Tip
			if tip == "" {
				tip = "-"
			}
			tags := strings.Join(c.Tags, ", ")
			if tags == "" {
				tags = "-"
			}
			fmt.Printf("Name:      %s\nTip:       %s\nTags:      %s\nScope:     %s\n", c.Name, tip, tags, scopeLabel(c.Scope))
			fmt.Printf("Created:   %s\nUpdated:   %s\nLast run:  %s\nRun count: %d\n", fmtTime(c.CreatedAt), fmtTime(c.UpdatedAt), fmtTime(c.LastRunAt), c.RunCount)
			fmt.Printf("Command:\n  %s\n", strings.ReplaceAll(c.Cmd, "\n", "\n  "))
			return nil
		},
	}
}

func newCopyCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "copy <name>",
		Aliases:           []string{"cp"},
		Short:             "Copy a command to the clipboard",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNames(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return fail(err)
			}
			defer st.Close()
			c, err := st.Resolve(args[0], currentScope())
			if err != nil {
				return fail(err)
			}
			if err := copyToClipboard(c.Cmd); err != nil {
				return fail(err)
			}
			fmt.Fprintf(os.Stderr, "bud: copied %q to the clipboard\n", c.Name)
			return nil
		},
	}
}

// ---- edit ----

func newEditCmd() *cobra.Command {
	var cmdStr, name, tip string
	var tags []string
	var global, here bool
	c := &cobra.Command{
		Use:               "edit <name> [--cmd <command>] [--name <new-name>] [--tip <text>] [--tag <tag>] [--global|--here]",
		Short:             "Edit a saved command (no flags: interactive)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNames(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			if global && here {
				return errors.New("--global and --here are mutually exclusive")
			}
			f := cmd.Flags()
			st, err := openStore()
			if err != nil {
				return fail(err)
			}
			defer st.Close()
			c, err := st.Resolve(args[0], currentScope())
			if err != nil {
				return fail(err)
			}

			if f.NFlag() == 0 {
				if !isTerminal(os.Stdin) {
					return fail(errors.New("no flags given and stdin is not a terminal"))
				}
				interactiveEdit(c)
			} else {
				if f.Changed("cmd") {
					c.Cmd = cmdStr
				}
				if f.Changed("name") {
					c.Name = strings.TrimSpace(name)
				}
				if f.Changed("tip") {
					c.Tip = tip
				}
				if f.Changed("tag") {
					c.Tags = normalizeTags(tags)
				}
				if global {
					c.Scope = ""
				}
				if here {
					c.Scope = currentScope()
				}
			}
			if strings.TrimSpace(c.Cmd) == "" || c.Name == "" {
				return fail(errors.New("command and name cannot be empty"))
			}
			if err := st.Update(c); err != nil {
				if isUnique(err) {
					return fail(fmt.Errorf("name %q already exists in that scope", c.Name))
				}
				return fail(err)
			}
			fmt.Fprintf(os.Stderr, "bud: updated %q (%s)\n", c.Name, scopeLabel(c.Scope))
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&cmdStr, "cmd", "", "new command")
	f.StringVar(&name, "name", "", "new name")
	f.StringVar(&tip, "tip", "", "new tip")
	f.StringArrayVar(&tags, "tag", nil, "replace tags (repeatable; --tag \"\" clears)")
	f.BoolVar(&global, "global", false, "make global")
	f.BoolVar(&here, "here", false, "scope to the current directory/repo")
	return c
}

func interactiveEdit(c *Command) {
	r := bufio.NewReader(os.Stdin)
	ask := func(label, cur string) string {
		fmt.Fprintf(os.Stderr, "%s [%s]: ", label, trunc(oneLine(cur), 70))
		line, _ := r.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		switch line {
		case "":
			return cur
		case "-":
			return ""
		}
		return line
	}
	fmt.Fprintln(os.Stderr, "Enter keeps the current value; '-' clears tip/tags.")
	c.Name = ask("Name", c.Name)
	c.Cmd = ask("Command", c.Cmd)
	c.Tip = ask("Tip", c.Tip)
	c.Tags = normalizeTags([]string{ask("Tags (comma separated)", strings.Join(c.Tags, ","))})
	switch strings.ToLower(ask("Scope: g=global, h=here", "keep")) {
	case "g":
		c.Scope = ""
	case "h":
		c.Scope = currentScope()
	}
}

// ---- rm ----

func newRmCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:               "rm <name> [-y]",
		Short:             "Remove a saved command",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNames(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return fail(err)
			}
			defer st.Close()
			c, err := st.Resolve(args[0], currentScope())
			if err != nil {
				return fail(err)
			}
			if !yes {
				if !isTerminal(os.Stdin) {
					return fail(errors.New("refusing to delete without -y when stdin is not a terminal"))
				}
				fmt.Fprintf(os.Stderr, "Remove %q (%s)? [y/N] ", c.Name, trunc(oneLine(c.Cmd), 50))
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
					fmt.Fprintln(os.Stderr, "bud: cancelled")
					return exitError{1}
				}
			}
			if err := st.Delete(c.ID); err != nil {
				return fail(err)
			}
			fmt.Fprintf(os.Stderr, "bud: removed %q\n", c.Name)
			return nil
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return c
}

// ---- ui ----

func newUICmd() *cobra.Command {
	var port int
	var noOpen, detach, stop bool
	c := &cobra.Command{
		Use:   "ui [--port <n>] [--no-open] [--detach | --stop]",
		Short: "Open the web dashboard",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case detach && stop:
				return errors.New("--detach and --stop are mutually exclusive")
			case stop:
				return stopUI(port)
			case detach:
				return detachUI(port, noOpen)
			}
			return runUI(port, noOpen)
		},
	}
	c.Flags().IntVar(&port, "port", defaultPort, "listen port")
	c.Flags().BoolVar(&noOpen, "no-open", false, "do not open a browser")
	c.Flags().BoolVar(&detach, "detach", false, "run the dashboard in the background")
	c.Flags().BoolVar(&stop, "stop", false, "stop the background dashboard on --port")
	return c
}
