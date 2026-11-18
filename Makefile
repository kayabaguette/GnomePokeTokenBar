VERSION ?= 0.1.1
UUID := poketokenbar@local
EXT_DIR := $(HOME)/.local/share/gnome-shell/extensions/$(UUID)
EXT_JS := $(shell find extension -name '*.js' -not -path 'extension/test/*' 2>/dev/null)

.PHONY: build test lint install uninstall pack rpm run-nested clean

build:
	cd helper && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o ../extension/bin/ptb-helper ./cmd/ptb-helper
	glib-compile-schemas extension/schemas

test:
	cd helper && go test -race ./...
	@for f in $(wildcard extension/test/*.test.js); do echo "gjs -m $$f"; gjs -m $$f || exit 1; done

lint:
	cd helper && go vet ./...
	@out="$$(cd helper && gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	@for f in $(EXT_JS); do node --input-type=module --check < $$f || { echo "syntax error: $$f"; exit 1; }; done

install: build
	rm -rf $(EXT_DIR)
	mkdir -p $(EXT_DIR)
	tar -C extension --exclude=./test -cf - . | tar -C $(EXT_DIR) -xf -

uninstall:
	rm -rf $(EXT_DIR)

pack: build
	mkdir -p dist
	rm -f dist/$(UUID).zip
	cd extension && zip -qr ../dist/$(UUID).zip . -x 'test/*'

# Installable RPM, built from a source tarball of the working tree inside dist/rpmbuild.
# Needs: sudo dnf install rpm-build golang
RPM_TOP := $(CURDIR)/dist/rpmbuild
RPM_NAME := gnome-shell-extension-poketokenbar

rpm:
	rm -rf $(RPM_TOP)
	mkdir -p $(RPM_TOP)/SOURCES
	tar --transform 's,^,$(RPM_NAME)-$(VERSION)/,' \
		--exclude=extension/bin --exclude=extension/test --exclude=extension/schemas/gschemas.compiled \
		-czf $(RPM_TOP)/SOURCES/$(RPM_NAME)-$(VERSION).tar.gz helper extension LICENSE README.md
	rpmbuild -bb --define "_topdir $(RPM_TOP)" --define "pkg_version $(VERSION)" packaging/$(RPM_NAME).spec
	cp $(RPM_TOP)/RPMS/*/$(RPM_NAME)-$(VERSION)-*.rpm dist/
	@ls -1 dist/$(RPM_NAME)-$(VERSION)-*.rpm

# Nested dev shell; requires the mutter-devkit package (sudo dnf install mutter-devkit).
# `gnome-shell --help` on this machine lists --devkit.
run-nested:
	PTB_STATE_DIR=$(CURDIR)/.dev-state dbus-run-session -- gnome-shell --devkit --wayland

clean:
	rm -rf extension/bin extension/schemas/gschemas.compiled dist
