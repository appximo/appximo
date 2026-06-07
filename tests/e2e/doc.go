//go:build e2e

// Package e2e holds the end-to-end client-scenario tests (build tag `e2e`):
// CRM, DIAN/CUFE, webhook, and attack scenarios land here in S38
// (context-docs/TESTING_PLAN.md). This placeholder gives the directory a real
// package under -tags e2e so `make test-e2e` is a clean "no test files" no-op
// until those tests are added — instead of failing with "matched no packages".
package e2e
