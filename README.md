# bud

A command library for managing shell commands across repositories and development environments.

`bud` organizes commands by scope, making them easy to find, execute, and share. Commands can be saved manually or captured as part of execution with `bud do`.

## How it works

Development often involves commands specific to a repository, environment, or task. These commands accumulate across projects and become difficult to track, particularly in large repositories or multi-repository development environments.

`bud` provides a persistent, searchable library for these commands. Each command has a name, description, optional tags, and a scope.

Commands are associated with the current Git repository when one is present. Outside a Git repository, the current working directory is used as the scope. Global commands are also supported.

This makes `bud` useful for individual development, team workflows, and AI assisted development.

## Use with AI assistants

AI assistants frequently generate commands tailored to a particular repository or development environment. As more of the development process is delegated to AI assistants and agents, the commands used along the way become harder to track, understand, and reuse. 

`bud do` combines execution and storage into one operation.

```bash
bud do --cmd 'docker compose logs -f api' \
  --name 'api-logs' \
  --tip 'Follow API container logs'
```

The command is saved to the local library and executed immediately through the host shell. It can subsequently be retrieved by name or search, keeping project specific commands accessible even as more of the development process is delegated to AI.

To integrate `bud` with an AI assistant, add the following instructions to `CLAUDE.md`, `AGENTS.md`, `.cursorrules`, etc., or your preferred system prompt:

```text
## Using bud for executable shell commands

`bud` is a local command library. It stores useful shell commands by name so they
can be found and reused later. `bud do` saves a command and executes it
immediately; use it whenever you are giving the user a shell command that you
intend them to run now.

Use this form:

bud do --cmd '<exact_command>' --name '<short_descriptive_name>' --tip '<brief_description>'

Rules:
- Wrap the complete command exactly as it should be executed in `--cmd`.
- Preserve the command's original arguments, quoting, operators, pipes, redirects,
  environment variables, and shell behavior. Do not simplify or rewrite it.
- Quote every string flag value so the user's current shell does not evaluate it
  before Bud receives it. Prefer single quotes for `--cmd`, `--name`, `--tip`,
  and other string values. For example, use `bud do --cmd 'echo "$MYVAR"'`,
  not `bud do --cmd "echo \"$MYVAR\""`, which expands `$MYVAR` too early. If a
  value contains a single quote, use a shell-safe alternative such as escaped
  single quotes or carefully escaped double quotes.
- Use a short, descriptive, reusable `--name` that reflects the command's purpose.
- Use `--tip` to briefly explain what the command does.
- Commands are scoped to the current Git repository by default. Use `--global`
  only for commands that are genuinely independent of any repository or directory.
- Do not use `bud do` for commands that are only being shown as examples,
  discussed hypothetically, or not intended to run immediately.
- Never store credentials, access tokens, private keys, or other sensitive values
  in the command or its options. Use placeholders and ask the user to provide
  sensitive values separately when needed.
- If a command contains shell state changes such as `cd`, `export`, or `source`,
  preserve them exactly. They affect the current shell only when the user has
  enabled bud's optional shell integration.
```

This makes command capture part of the normal development workflow without requiring a separate cataloging step.

## Scoping

Commands are organized by their working directory.

- **Repository scope:** Commands saved inside a Git repository are associated with its root directory.
- **Directory scope:** Outside a Git repository, commands are associated with the current working directory.
- **Global scope:** Commands explicitly saved with `--global` are available from any directory.

When resolving a command, `bud` checks the current scope first, then the global scope.

This is particularly useful when working across multiple repositories or maintaining larger projects with different build, test, deployment, and maintenance procedures.

## Sharing commands

Each user maintains a local SQLite database located at `~/.config/bud/db.sqlite`. Commands can be exported to a `bud.yaml` file and committed to a repository.

Example `bud.yaml`:

```yaml
commands:
  - name: rebuild
    command: go clean -cache && go mod tidy && go build -ldflags="-s -w" -o bin/bud ./...
    tip: Clean the Go build cache, resolve dependencies, and rebuild the application
    tags: [build, maintenance]

  - name: find-large-files
    command: find . -type f -size +100M -exec ls -lh {} \; | sort -k5 -h
    tip: Find files larger than 100 MB and sort them by size
    tags: [filesystem, diagnostics]
    scope: global
```

Other users can import those commands into their own local libraries using `bud sync`.

```bash
bud export
git add bud.yaml
git commit -m "Add project bud commands"
```

On another development environment:

```bash
git pull
bud sync
```

Export and synchronization support three scope selections:

- Default: export commands belonging to the current repository or directory.
- `--with-global`: include global commands.
- `--all`: include commands from every scope.

Global commands retain their global designation when exported. Repository specific commands are imported into the receiving developer's current repository scope, regardless of the original machine's directory structure.

`bud sync` compares incoming commands against existing entries. Identical commands are left unchanged, and conflicting commands can be reviewed before replacement or overwritten with `--overwrite`. Commands not present in the import file are not deleted.

This allows teams to maintain a version controlled command library alongside their source code without sharing a database or requiring centralized infrastructure.

## Command reference

Run `bud --help` or `bud <command> --help` for the complete flags and usage details.

## Installation

### Build from source

Requires Go 1.22 or later.

```bash
git clone https://github.com/wcuny/bud.git
cd bud
make build
```

Install the resulting binary:

```bash
sudo install -m 755 bud /usr/local/bin/bud
```

### Tab completion

Tab completion is available. Install the completion scripts with:

```bash
bud completion install
```

### Shell execution

`bud do` executes commands through `$SHELL -c` (falling back to the platform shell when `$SHELL` is unavailable).

Standard input, output, and error are connected directly to the child process. This preserves normal shell behavior, including pipes, redirections, environment variables, and terminal signals.

### Optional shell integration

By default, `bud run` and `bud do` execute commands in a child shell. This means
commands such as `cd`, `export`, and `source` do not affect your current terminal.

To let saved commands modify your current interactive shell, add the following
line to your shell startup file:

For Zsh, add this to `~/.zshrc`:

```bash
eval "$(bud shell-init zsh)"
```

For Bash, add this to `~/.bashrc`:

```bash
eval "$(bud shell-init bash)"
```

Restart your shell, or run the line once manually. With shell integration
enabled, `bud run` and `bud do` execute saved commands in your current shell.

## Web UI

Start the local web UI:

```bash
bud ui
```

The web UI is served at:

http://localhost:11112

Frontend assets are embedded in the Go binary using `embed.FS`.

## License

MIT License. See [LICENSE](LICENSE).