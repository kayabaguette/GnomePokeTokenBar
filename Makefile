VERSION ?= 0.0.1
UUID := poketokenbar@local
EXT_DIR := $(HOME)/.local/share/gnome-shell/extensions/$(UUID)
EXT_JS := $(shell find extension -name '*.js' -not -path 'extension/test/*' 2>/dev/null)

.PHONY: build test lint install uninstall pack run-nested clean

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

# Nested dev shell; requires the mutter-devkit package (sudo dnf install mutter-devkit).
# `gnome-shell --help` on this machine lists --devkit.
run-nested:
	PTB_STATE_DIR=$(CURDIR)/.dev-state dbus-run-session -- gnome-shell --devkit --wayland

clean:
	rm -rf extension/bin extension/schemas/gschemas.compiled dist
