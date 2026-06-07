.PHONY: build lint run build-pgo collect-profile \
	test test-integration test-e2e test-perf test-security test-all bench

build:
	go build ./...

lint:
	golangci-lint run ./...

run:
	go run ./cmd/appitools/main.go

# ─────────────────────────────────────────────────────────────────────────────
# Test lanes (context-docs/TESTING_PLAN.md)
# ─────────────────────────────────────────────────────────────────────────────

# test: fast unit lane — race detector + -short.
# The S37 tests/ suites are build-tagged (integration / e2e) and excluded here.
# NOTE: a few legacy pkg suites (pkg/db, pkg/graphql, pkg/migration,
# pkg/controlplane, internal/handlers, pkg/benchmark) still use testcontainers
# without a -short guard, so this lane currently needs Docker until those gain
# guards/tags. See TESTING_PLAN follow-up.
test:
	go test ./... -race -short

# test-integration: DB-backed integration suite (real Postgres via testcontainers).
test-integration:
	go test -tags integration -race -count=1 ./tests/integration/...

# test-e2e: full client scenarios (S38+).
test-e2e:
	go test -tags e2e -race -count=1 ./tests/e2e/...

# test-perf: k6 SLO gate. Exits 99 if p95>=15ms or error_rate>=1%.
# Requires a running server + BENCH_TOKEN (mint with `appitools token`).
# Override RATE/DURATION/TARGET_URL/TENANT_ID via env. See tests/performance/README.md.
test-perf:
	k6 run tests/performance/sustained_2krps.js

# test-security: DAST (nuclei + ZAP) — manual/nightly, wired in S40.
test-security:
	@echo "test-security: nuclei + ZAP DAST is scheduled for S40 (nightly), not yet wired."
	@echo "See context-docs/TESTING_PLAN.md and tests/security/."

# test-all: unit + integration + perf (matches TESTING_PLAN).
test-all: test test-integration test-perf

# bench: Go benchmarks. For A/B regression, save two runs and compare with benchstat:
#   go test -bench=. -benchmem -run='^$$' ./... | tee new.txt
#   benchstat old.txt new.txt
bench:
	go test -bench=. -benchmem -run='^$$' ./...

# ─────────────────────────────────────────────────────────────────────────────
# PGO
# ─────────────────────────────────────────────────────────────────────────────

# collect-profile: sample 30 s of CPU from the running dev server (port 6060)
# and save it as default.pgo. Run this while the server is under load.
collect-profile:
	@echo "Collecting 30s CPU profile from dev server (APPITOOLS_ENV=development)..."
	curl -sf "http://localhost:6060/debug/pprof/profile?seconds=30" -o default.pgo
	@echo "Profile saved to default.pgo — now run: make build-pgo"

# build-pgo: compile with the profile collected by collect-profile.
# Requires default.pgo to exist (run collect-profile first).
# -trimpath strips local paths; -ldflags="-s -w" drops the symbol table + DWARF
# (~30% smaller binary). The runtime pclntab is kept, so runtime.CallersFrames —
# and therefore the error trace explorer's symbolized stacks — still work.
build-pgo: default.pgo
	go build -pgo=default.pgo -trimpath -ldflags="-s -w" -o appitools ./cmd/appitools/
	@echo "PGO binary written to ./appitools"
