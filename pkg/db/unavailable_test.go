package db

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/puddle/v2"
	"github.com/sony/gobreaker"
)

// LOOSE-ENDS-SWEEP-S1 — 503 vs 500.
//
// The production-stack benchmark recorded a 15 s PostgreSQL outage as a run of
// clean 500s and called it "one honest wart" (docs/BENCHMARKS.md §6): a 500 tells
// a client "your request is broken, do not retry" when the truth was "I am down,
// retry later". These tests pin the classification in both directions — the
// second half matters more, because over-classifying would make a real bug look
// transient and get silently retried forever.

func TestClassify_Unavailable(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"the query deadline elapsed", context.DeadlineExceeded},
		{"the circuit breaker is open", gobreaker.ErrOpenState},
		{"the breaker is probing (half-open)", gobreaker.ErrTooManyRequests},
		{"connection refused (PostgreSQL is stopped)", syscall.ECONNREFUSED},
		{"connection reset mid-query", syscall.ECONNRESET},
		{"host unreachable", syscall.EHOSTUNREACH},
		{"network unreachable", syscall.ENETUNREACH},
		{"broken pipe", syscall.EPIPE},
		{"the wire died mid-query", io.ErrUnexpectedEOF},
		{"EOF from the server", io.EOF},
		{"a dial failure", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}},
		{"the pool was closed", puddle.ErrClosedPool},
		{"08006 connection_failure", &pgconn.PgError{Code: "08006"}},
		{"08003 connection_does_not_exist", &pgconn.PgError{Code: "08003"}},
		{"08001 sqlclient_unable_to_establish", &pgconn.PgError{Code: "08001"}},
		{"53300 too_many_connections", &pgconn.PgError{Code: "53300"}},
		{"53200 out_of_memory", &pgconn.PgError{Code: "53200"}},
		{"53100 disk_full", &pgconn.PgError{Code: "53100"}},
		{"57P01 admin_shutdown", &pgconn.PgError{Code: "57P01"}},
		{"57P02 crash_shutdown", &pgconn.PgError{Code: "57P02"}},
		{"57P03 cannot_connect_now (standby recovering)", &pgconn.PgError{Code: "57P03"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(c.err); !IsUnavailable(got) {
				t.Fatalf("expected ErrUnavailable (→503), got %v", got)
			}
			// Wrapped the way a real call site returns it.
			wrapped := fmt.Errorf("query tasks: %w", c.err)
			if got := classify(wrapped); !IsUnavailable(got) {
				t.Fatalf("wrapped: expected ErrUnavailable, got %v", got)
			}
		})
	}
}

// The other direction, and the one that protects correctness: a real error must
// NOT become a 503, or a genuine bug would be reported as transient and retried
// forever by well-behaved clients.
func TestClassify_NotUnavailable(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"23505 unique_violation (→409)", &pgconn.PgError{Code: "23505"}},
		{"23503 foreign_key_violation (→409)", &pgconn.PgError{Code: "23503"}},
		{"42P01 undefined_table (→400 invalid tenant)", &pgconn.PgError{Code: "42P01"}},
		{"42703 undefined_column (→422)", &pgconn.PgError{Code: "42703"}},
		{"22P02 invalid_text_representation (→400)", &pgconn.PgError{Code: "22P02"}},
		{"42601 syntax_error (a real bug)", &pgconn.PgError{Code: "42601"}},
		{"40001 serialization_failure — the caller lost a race, not an outage", &pgconn.PgError{Code: "40001"}},
		{"40P01 deadlock_detected — same reasoning", &pgconn.PgError{Code: "40P01"}},
		{"a plain error", errors.New("something went wrong")},
		{"context.Canceled (the CLIENT went away)", context.Canceled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify(c.err)
			if IsUnavailable(got) {
				t.Fatalf("must NOT be classified as unavailable: %v", got)
			}
			if c.err == nil && got != nil {
				t.Fatalf("nil must stay nil, got %v", got)
			}
		})
	}
}

// classify must preserve the cause so the downstream classifiers
// (UniqueViolationField, IsMissingTenant, …) still see their SQLSTATE.
func TestClassify_PreservesTheCause(t *testing.T) {
	orig := &pgconn.PgError{Code: "08006", Message: "server closed the connection"}
	got := classify(orig)
	var pgErr *pgconn.PgError
	if !errors.As(got, &pgErr) || pgErr.Code != "08006" {
		t.Fatalf("the original PgError must remain unwrappable, got %v", got)
	}
}
