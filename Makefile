# touring-diary build targets. See README.md.

BIN      := bin/touring-diary
PKG      := ./cmd/touring-diary
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PLATFORMS := linux/amd64 linux/arm64 darwin/arm64 darwin/amd64 windows/amd64

# Demo inputs: this repository's sample trip. The config sets the title and
# leaves the Tampere day out of the initial map view.
GPX   ?= gpx
NOTES ?= events_log
MEDIA ?= media
OUT   ?= dist
CONFIG ?= lapland-2026.json
ADDR  ?= 127.0.0.1:8090

.PHONY: build release test lint demo serve clean

# Default: dynamic build, which loads native libheif at run time when it is
# installed (much faster HEIC decoding).
build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BIN) $(PKG)

# Static release binaries (pure Go, WASM HEIC decoder) for each platform in
# bin/<os>-<arch>/. Restrict with e.g. make release PLATFORMS=linux/amd64
release:
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "building bin/$$os-$$arch/touring-diary$$ext"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -tags nodynamic -trimpath \
			-ldflags "-s -w -X main.version=$(VERSION)" \
			-o bin/$$os-$$arch/touring-diary$$ext $(PKG); \
	done

test:
	go test ./...

lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...

demo: build
	$(BIN) build --gpx $(GPX) --notes $(NOTES) --media $(MEDIA) --out $(OUT) --config $(CONFIG)

serve: build
	$(BIN) serve --addr $(ADDR) $(OUT)

clean:
	rm -rf bin
