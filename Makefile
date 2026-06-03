.PHONY: build test lint run build-pgo collect-profile

build:
	go build ./...

test:
	go test ./... -race

lint:
	golangci-lint run ./...

run:
	go run ./cmd/appitools/main.go

# collect-profile: sample 30 s of CPU from the running dev server (port 6060)
# and save it as default.pgo. Run this while the server is under load.
collect-profile:
	@echo "Collecting 30s CPU profile from dev server (APPITOOLS_ENV=development)..."
	curl -sf "http://localhost:6060/debug/pprof/profile?seconds=30" -o default.pgo
	@echo "Profile saved to default.pgo — now run: make build-pgo"

# build-pgo: compile with the profile collected by collect-profile.
# Requires default.pgo to exist (run collect-profile first).
build-pgo: default.pgo
	go build -pgo=default.pgo -o appitools ./cmd/appitools/
	@echo "PGO binary written to ./appitools"
