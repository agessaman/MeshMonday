SHELL := /bin/zsh

ENV_FILE := .env.local
FIXTURE ?= fixtures/mqtt/monday-sea.jsonl

.PHONY: setup run dev build test test-integration lint dev-seed replay-checkins restore-checkin-packets vacuum clean

setup:
	@mkdir -p data bin
	@if [ ! -f "$(ENV_FILE)" ]; then cp scripts/dev_env.example "$(ENV_FILE)"; echo "created $(ENV_FILE) from scripts/dev_env.example"; fi
	@go version

run: setup
	@set -a; source $(ENV_FILE); set +a; go run ./cmd/server

dev: setup
	@if ! command -v air >/dev/null 2>&1; then \
		echo "installing air hot-reload tool..."; \
		go install github.com/air-verse/air@latest; \
	fi
	@set -a; source $(ENV_FILE); set +a; $$(command -v air || echo "$$(go env GOPATH)/bin/air") -c .air.toml

build:
	@go build -o ./bin/meshmonday ./cmd/server

test:
	@go test ./...

test-integration:
	@go test ./... -run Integration

lint:
	@gofmt -w $$(git ls-files '*.go')
	@go vet ./...

dev-seed: setup
	@set -a; source $(ENV_FILE); set +a; go run ./cmd/devseed

replay-checkins: setup
	@set -a; source $(ENV_FILE); set +a; go run ./cmd/replay -fixture $(FIXTURE)

restore-checkin-packets: setup
	@set -a; source $(ENV_FILE); set +a; go run ./cmd/restorecheckinpackets

vacuum: setup
	@set -a; source $(ENV_FILE); set +a; sqlite3 "$$SQLITE_PATH" 'VACUUM;'

clean:
	@rm -rf ./bin ./data/*_test.db
