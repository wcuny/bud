BINARY  := bud
VERSION := $(shell sed -n 's/^const version = "\(.*\)"/\1/p' main.go)
PREFIX  ?= /usr/local
DESTDIR ?=

.PHONY: build completions completion-bash completion-zsh completion-fish install clean dist rpm

build: $(BINARY)

$(BINARY): $(wildcard *.go) web/index.html go.mod
	go build -o $(BINARY) .

# Completion scripts are generated from the command definitions in the binary,
# so they cannot drift from the CLI.
completions: completion-bash completion-zsh completion-fish

completion-bash: $(BINARY)
	mkdir -p completions
	./$(BINARY) completion bash > completions/bud.bash

completion-zsh: $(BINARY)
	mkdir -p completions
	./$(BINARY) completion zsh > completions/_bud

completion-fish: $(BINARY)
	mkdir -p completions
	./$(BINARY) completion fish > completions/bud.fish

# System-wide install (the RPM uses this with DESTDIR and PREFIX=/usr).
# Shell config files are never touched.
install: build completions
	mkdir -p $(DESTDIR)$(PREFIX)/bin
	install -m 0755 $(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	mkdir -p $(DESTDIR)$(PREFIX)/share/bash-completion/completions
	install -m 0644 completions/bud.bash $(DESTDIR)$(PREFIX)/share/bash-completion/completions/bud
	mkdir -p $(DESTDIR)$(PREFIX)/share/zsh/site-functions
	install -m 0644 completions/_bud $(DESTDIR)$(PREFIX)/share/zsh/site-functions/_bud
	mkdir -p $(DESTDIR)$(PREFIX)/share/fish/vendor_completions.d
	install -m 0644 completions/bud.fish $(DESTDIR)$(PREFIX)/share/fish/vendor_completions.d/bud.fish

clean:
	rm -rf $(BINARY) completions dist

# Source tarball for rpmbuild, with vendored modules so the RPM builds offline.
dist:
	rm -rf dist/$(BINARY)-$(VERSION)
	mkdir -p dist/$(BINARY)-$(VERSION)
	cp -r *.go web go.mod go.sum Makefile README.md LICENSE dist/$(BINARY)-$(VERSION)/
	cd dist/$(BINARY)-$(VERSION) && go mod vendor
	tar -C dist -czf dist/$(BINARY)-$(VERSION).tar.gz $(BINARY)-$(VERSION)

rpm: dist
	rpmbuild -ba packaging/bud.spec \
		--define "_topdir $(CURDIR)/dist/rpmbuild" \
		--define "_sourcedir $(CURDIR)/dist" \
		--define "bud_version $(VERSION)"