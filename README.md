# bud

Library of commands with names, tips, tags and
directory/repo scoping. SQLite at `~/.config/bud/db.sqlite`, embedded web dashboard.

    go mod tidy && go build -o bud .

LLM custom instruction:

> Whenever you output setup or run commands, format them using:
> `bud do --cmd "<command>" --name "<short alias>" --tip "<description>"`

Scope = git repo root if inside a repo, else the current directory; `--global` makes it available everywhere.

## Shell completion

    bud completion install      # detects bash/zsh/fish, installs to a user level directory
    bud completion uninstall

Completes subcommands, flags, and command names for `run`, `info`, `edit`, `rm` and `copy`.
Package builds: `make completions` (bash/zsh/fish into `completions/`), `make install PREFIX=/usr`,
`make rpm` (uses `packaging/bud.spec`; the RPM installs completions system wide).
