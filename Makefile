GO_TOOLCHAIN ?= go1.26.7
GO ?= go
PNPM ?= pnpm
VERSION_FILE := cmd/ycy/VERSION
CURRENT_VERSION := $(shell tr -d '\r\n' < $(VERSION_FILE))
RELEASE_DIR := release/$(CURRENT_VERSION)

GO_FIND = find acceptance cmd internal pkg tools/hookctl tools/release tools/release-artifacts tools/prepare-frp-runtime tools/web-browser-harness web -path '*/node_modules' -prune -o -type f -name '*.go'

.PHONY: help bootstrap hooks-install hooks-doctor hooks-uninstall fmt check check-web check-go check-locks check-terminal acceptance acceptance-web acceptance-terminal command-surface command-surface-update build cross-build release release-clean release-candidate release-untracked web-browser-harness ensure-web-deps ensure-web-dist prepare-7zip prepare-7zip-all

help:
	@printf '%s\n' 'Targets: bootstrap, hooks-install, hooks-doctor, hooks-uninstall, fmt, check, check-terminal, acceptance, acceptance-web, acceptance-terminal, command-surface, command-surface-update, build, cross-build, release, release-candidate, web-browser-harness'

bootstrap:
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) version
	@node --version
	@$(PNPM) --version
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off $(GO) mod download
	@cd tools/lefthook && GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off $(GO) mod download all
	@mkdir -p tools/lefthook/bin
	@cd tools/lefthook && GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off GOPROXY=off $(GO) build -mod=readonly -o bin/lefthook github.com/evilmartians/lefthook/v2
	@$(PNPM) --dir web install --frozen-lockfile

hooks-install:
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off $(GO) run ./tools/hookctl install

hooks-doctor:
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off $(GO) run ./tools/hookctl doctor

hooks-uninstall:
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off $(GO) run ./tools/hookctl uninstall

fmt:
	@$(GO_FIND) -exec gofmt -w {} +
	@$(PNPM) --dir web exec eslint --fix .

ensure-web-deps:
	@test -d web/node_modules || { printf '%s\n' 'web dependencies are unavailable; run make bootstrap'; exit 1; }

ensure-web-dist:
	@test -d web/dist || { printf '%s\n' 'web output is unavailable; run make build or make check-web'; exit 1; }

check-web: ensure-web-deps
	@$(PNPM) --dir web run lint
	@$(PNPM) --dir web run typecheck
	@$(PNPM) --dir web run test
	@$(PNPM) --dir web run build

prepare-7zip:
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off $(GO) run ./tools/prepare-sevenzip --target "$$($(GO) env GOOS)-$$($(GO) env GOARCH)"

prepare-7zip-all: prepare-7zip
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off $(GO) run ./tools/prepare-sevenzip --all

check-go: check-web ensure-web-dist prepare-7zip
	@unformatted="$$($(GO_FIND) -exec gofmt -l {} +)"; test -z "$$unformatted" || { printf '%s\n%s\n' 'Run make fmt; these Go files are not formatted:' "$$unformatted"; exit 1; }
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 $(GO) vet ./...
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 $(GO) test ./...

check-locks: ensure-web-deps
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off $(GO) mod verify
	@cd tools/lefthook && GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off $(GO) mod verify
	@$(PNPM) --dir web install --frozen-lockfile --offline --ignore-scripts

check: check-locks check-go

check-terminal:
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 $(GO) test -count=1 ./internal/terminal ./internal/terminaltest ./pkg/cmd/root ./pkg/cmd/export/env ./pkg/cmd/config/fork/... ./pkg/cmd/config/cm/... ./pkg/cmd/git/... ./pkg/cmd/diff ./pkg/cmd/fs ./pkg/cmd/rm ./pkg/cmd/run ./pkg/cmd/tunnel/... ./pkg/cmd/upgrade ./pkg/cmd/zip

acceptance:
	@GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1 -tags=acceptance ./acceptance/...

acceptance-terminal:
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 $(GO) test -count=1 -tags=acceptance ./acceptance/...

acceptance-web: check-web prepare-7zip
	@GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1 -tags=acceptance ./acceptance/web

command-surface:
	@GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1 ./pkg/cmd/root -run '^TestCommandSurface$$'

command-surface-update:
	@YCY_UPDATE_COMMAND_SURFACE=1 GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1 ./pkg/cmd/root -run '^TestCommandSurface$$'

build: check-web prepare-7zip
	@mkdir -p build
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-X main.version=$(CURRENT_VERSION)" -o build/ycy ./cmd/ycy

cross-build: check-web prepare-7zip-all
	@mkdir -p build/cross
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build -trimpath -ldflags "-X main.version=$(CURRENT_VERSION)" -o build/cross/ycy-macos-x64 ./cmd/ycy
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -ldflags "-X main.version=$(CURRENT_VERSION)" -o build/cross/ycy-macos-arm64 ./cmd/ycy
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "-X main.version=$(CURRENT_VERSION)" -o build/cross/ycy-linux-x64 ./cmd/ycy
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "-X main.version=$(CURRENT_VERSION)" -o build/cross/ycy-linux-arm64 ./cmd/ycy
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags "-X main.version=$(CURRENT_VERSION)" -o build/cross/ycy-windows-x64.exe ./cmd/ycy
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GO) build -trimpath -ldflags "-X main.version=$(CURRENT_VERSION)" -o build/cross/ycy-windows-arm64.exe ./cmd/ycy

release:
	@set -e; release_tool="$$(mktemp -t ycy-release.XXXXXX)"; trap 'rm -f "$$release_tool"' EXIT; GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off $(GO) build -o "$$release_tool" ./tools/release; DRY_RUN="$(DRY_RUN)" "$$release_tool"

release-clean:
	@test -n "$(CURRENT_VERSION)" || { printf '%s\n' 'release-candidate requires a non-empty cmd/ycy/VERSION'; exit 1; }
	@printf '%s\n' "$(CURRENT_VERSION)" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$$' || { printf '%s\n' 'cmd/ycy/VERSION must contain a stable X.Y.Z version'; exit 1; }
	@for candidate in web/dist web/node_modules build .cache .tmp release internal/sevenzipruntime/payload tools/lefthook/bin; do \
		test ! -e "$$candidate" || { printf '%s\n' "release-candidate requires a clean checkout; found $$candidate"; exit 1; }; \
	done

release-candidate: release-clean
	@$(MAKE) bootstrap
	@$(PNPM) --dir web run build
	@$(MAKE) prepare-7zip-all
	@mkdir -p $(RELEASE_DIR)
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build -trimpath -ldflags "-X main.version=$(CURRENT_VERSION)" -o $(RELEASE_DIR)/ycy-macos-x64 ./cmd/ycy
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -ldflags "-X main.version=$(CURRENT_VERSION)" -o $(RELEASE_DIR)/ycy-macos-arm64 ./cmd/ycy
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "-X main.version=$(CURRENT_VERSION)" -o $(RELEASE_DIR)/ycy-linux-x64 ./cmd/ycy
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "-X main.version=$(CURRENT_VERSION)" -o $(RELEASE_DIR)/ycy-linux-arm64 ./cmd/ycy
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags "-X main.version=$(CURRENT_VERSION)" -o $(RELEASE_DIR)/ycy-windows-x64.exe ./cmd/ycy
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GO) build -trimpath -ldflags "-X main.version=$(CURRENT_VERSION)" -o $(RELEASE_DIR)/ycy-windows-arm64.exe ./cmd/ycy
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off $(GO) run ./tools/release-artifacts --directory $(RELEASE_DIR)
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off $(GO) run ./tools/release-artifacts --verify --version $(CURRENT_VERSION) --directory $(RELEASE_DIR)
	@$(MAKE) release-untracked

release-untracked:
	@tracked="$$(git ls-files -- web/dist web/node_modules build .cache .tmp release internal/sevenzipruntime/payload tools/lefthook/bin)"; test -z "$$tracked" || { printf '%s\n%s\n' 'generated candidate output is tracked:' "$$tracked"; exit 1; }

web-browser-harness: check-web
	@GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 $(GO) run ./tools/web-browser-harness
