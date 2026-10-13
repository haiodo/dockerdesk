# gowt builds without cgo on every platform (it loads the OS libraries at run time).
export CGO_ENABLED := 0

BIN  := bin
NAME := dockerdesk
HOSTOS := $(shell go env GOOS)
EXT  := $(if $(filter windows,$(HOSTOS)),.exe)

.PHONY: build run test vet cross clean shots shots-macos shots-host \
	build-darwin-arm64 build-darwin-amd64 build-linux-amd64 build-windows-amd64

build:
	go build -o $(BIN)/$(NAME)$(EXT) .

run:
	go run .

# Unit tests only: live_test.go skips itself unless DOCKERDESK_LIVE=1.
test:
	go test ./...

vet:
	go vet ./...

cross: build-darwin-arm64 build-darwin-amd64 build-linux-amd64 build-windows-amd64

build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build -o $(BIN)/$(NAME)-darwin-arm64 .
build-darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build -o $(BIN)/$(NAME)-darwin-amd64 .
build-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -o $(BIN)/$(NAME)-linux-amd64 .
build-windows-amd64:
	GOOS=windows GOARCH=amd64 go build -o $(BIN)/$(NAME)-windows-amd64.exe .

clean:
	rm -rf $(BIN)

# README screenshots (docs/img/*.png) of the real window; needs a reachable Docker daemon with some
# containers and opens a window. GOWT_SNAP writes the first window as PNG and makes Run return.
# Run it on each OS you want a picture for: macOS writes light and dark, Linux and Windows one each.
IMG := docs/img
SNAPDELAY ?= 4000

shots: $(if $(filter darwin,$(HOSTOS)),shots-macos,shots-host)

shots-host:
	@mkdir -p $(IMG)
	GOWT_SNAP=$(IMG)/$(NAME)-$(HOSTOS).png GOWT_SNAP_DELAY=$(SNAPDELAY) go run .

# Dark mode is switched through System Events and restored to the original value afterwards.
shots-macos:
	@mkdir -p $(IMG)
	@orig=$$(osascript -e 'tell application "System Events" to tell appearance preferences to get dark mode'); \
	trap "osascript -e 'tell application \"System Events\" to tell appearance preferences to set dark mode to $$orig'" EXIT; \
	osascript -e 'tell application "System Events" to tell appearance preferences to set dark mode to false'; sleep 1; \
	GOWT_SNAP=$(IMG)/$(NAME)-macos-light.png GOWT_SNAP_DELAY=$(SNAPDELAY) go run . || exit 1; \
	osascript -e 'tell application "System Events" to tell appearance preferences to set dark mode to true'; sleep 1; \
	GOWT_SNAP=$(IMG)/$(NAME)-macos-dark.png GOWT_SNAP_DELAY=$(SNAPDELAY) go run .
