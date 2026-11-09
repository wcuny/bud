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
bud do --cmd "docker compose logs -f api" \
  --name "api-logs" \
  --tip "Follow API container logs"
```

The command is saved to the local library and executed immediately through the host shell. It can subsequently be retrieved by name or search, keeping project specific commands accessible even as more of the development process is delegated to AI.

To integrate `bud` with an AI assistant, add the following instructions to `CLAUDE.md`, `.cursorrules`, `~/.codex/config.toml`, etc., or your preferred system prompt:

```text
When providing shell commands that should be executed, use bud do:

bud do --cmd "<exact_command>" --name "<short_alias>" --tip "<concise_description>"

Follow these rules:
- bud do executes the command immediately and saves it to the command library. Do not use it for commands that are only being discussed or presented as examples.
- Commands are scoped to the current Git repository by default. Preserve this behavior unless the command is genuinely independent of any project.
- Use --global only for commands that are completely repo-agnostic.
- Use short, descriptive names that are easy to remember and reuse.
- Do not save commands containing credentials, tokens, or other sensitive values.
- Preserve the original command and its arguments. Do not change its behavior when wrapping it in bud do.
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

### Shell execution

`bud do` executes commands through `$SHELL -c` (falling back to the platform shell when `$SHELL` is unavailable).

Standard input, output, and error are connected directly to the child process. This preserves normal shell behavior, including pipes, redirections, environment variables, and terminal signals.

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