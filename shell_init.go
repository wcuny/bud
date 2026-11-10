package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

func newShellInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "shell-init <bash|zsh>",
		Short:     "Print optional shell integration",
		Hidden:    true,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash", "zsh":
				_, err := io.WriteString(cmd.OutOrStdout(), shellInitScript(args[0]))
				return err
			default:
				return errors.New("shell-init supports bash and zsh")
			}
		},
	}
}

func shellInitScript(shell string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# bud shell integration for %s\n", shell)
	b.WriteString(`bud() {
    case "$1" in
    run|do)
        if [ "$#" -eq 1 ]; then
            command bud "$@"
            return $?
        fi
        local _bud_command
        _bud_command=$(command bud "$1" --emit "${@:2}") || return $?
        eval "$_bud_command"
        ;;
    *)
        command bud "$@"
        ;;
    esac
}
`)
	return b.String()
}
