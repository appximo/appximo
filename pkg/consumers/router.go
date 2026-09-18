package consumers

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog"

	"github.com/appximo/appximo/pkg/worker"
)

// Router is the COEXISTENCE mechanism for a shared outbox carrying multiple event
// types. It is a worker.Processor that dispatches each row to the registered
// consumer whose topic rule matches — and it implements worker.TopicOwner, so a
// Drain through a Router only ever CLAIMS the registered topics: a foreign topic
// is never locked, never acked, never marked sent (AUTO-1, AUTOMATIZACION-S1).
//
// WHY a dispatcher and not one worker per mode: the outbox is one table drained by
// competing consumers under FOR UPDATE SKIP LOCKED. Before topic-scoped claiming,
// a single-purpose worker (xlsx-only, email-only) ACKED the topics it didn't own —
// so if an xlsx worker and an email worker drained the SAME outbox, whichever
// claimed a row first destroyed it for the other: silent event loss. Topic-scoped
// claiming removes that trap at the SQL layer; the Router remains the way ONE
// process handles several event types, scaled horizontally by running N identical
// Router workers (SKIP LOCKED still gives each a disjoint slice).
//
// Matching is by exact topic, a "prefix." prefix rule, or a ".suffix" suffix rule;
// the first registered rule that matches wins, so register more specific rules
// first.
//
// An UNMATCHED topic is an ERROR, never an ack (nobody in the industry marks a
// job done because no handler exists — Sidekiq raises and retries). In practice
// the scoped claim means an unmatched row is never even claimed; the error path
// is defense in depth for a rule set whose SQL patterns and match functions
// disagree, and for callers that bypass the scoping. To deliberately drop a
// topic, register it with Discard — an explicit, logged decision.
type Router struct {
	routes []routerEntry
	topics worker.TopicSet
	owners []worker.TopicOwner // dynamic owners (HandleOwner), merged at Topics()
	log    zerolog.Logger
}

type routerEntry struct {
	match func(topic string) bool
	proc  worker.Processor
	label string
}

// NewRouter builds an empty Router. Add consumers with Handle / HandlePrefix /
// HandleSuffix; drop topics on purpose with Discard.
func NewRouter(log zerolog.Logger) *Router { return &Router{log: log} }

// Handle routes the EXACT topic to proc. Returns r for chaining.
func (r *Router) Handle(topic string, proc worker.Processor) *Router {
	r.routes = append(r.routes, routerEntry{
		match: func(t string) bool { return t == topic },
		proc:  proc,
		label: topic,
	})
	r.topics.Exact = append(r.topics.Exact, topic)
	return r
}

// HandlePrefix routes every topic starting with prefix (e.g. prefix "filejobs."
// matches "filejobs.created", "filejobs.updated") to proc. Returns r for chaining.
func (r *Router) HandlePrefix(prefix string, proc worker.Processor) *Router {
	r.routes = append(r.routes, routerEntry{
		match: func(t string) bool { return strings.HasPrefix(t, prefix) },
		proc:  proc,
		label: prefix + "*",
	})
	r.topics.Prefixes = append(r.topics.Prefixes, prefix)
	return r
}

// HandleSuffix routes every topic ending with suffix (e.g. ".created" matches
// every resource's create event) to proc. Returns r for chaining.
func (r *Router) HandleSuffix(suffix string, proc worker.Processor) *Router {
	r.routes = append(r.routes, routerEntry{
		match: func(t string) bool { return strings.HasSuffix(t, suffix) },
		proc:  proc,
		label: "*" + suffix,
	})
	r.topics.Suffixes = append(r.topics.Suffixes, suffix)
	return r
}

// OwnerProcessor is a Processor that declares its own (possibly DYNAMIC) topic
// ownership — e.g. the workflow event consumer, whose topics change when a
// tenant deploys a schema.
type OwnerProcessor interface {
	worker.Processor
	worker.TopicOwner
}

// HandleOwner routes every topic the OwnerProcessor currently owns to it. The
// ownership is re-read on every match and on every Topics() call, so a consumer
// whose set changes at runtime stays correctly scoped.
func (r *Router) HandleOwner(label string, po OwnerProcessor) *Router {
	r.routes = append(r.routes, routerEntry{
		match: func(t string) bool { return po.Topics().Matches(t) },
		proc:  po,
		label: label,
	})
	r.owners = append(r.owners, po)
	return r
}

// Discard acknowledges the EXACT topic without doing anything — the explicit,
// logged way to say "this event type is deliberately dropped here". It exists so
// that dropping is always a written decision; an UNREGISTERED topic is an error.
func (r *Router) Discard(topic string) *Router {
	log := r.log
	return r.Handle(topic, worker.ProcessorFunc(func(_ context.Context, row worker.Row) error {
		log.Info().Int64("id", row.ID).Str("topic", row.Topic).Msg("router: topic explicitly discarded (registered with Discard)")
		// state='discarded' with the reason — never 'sent' (VOZ-DELTA-S1).
		return worker.Discard("topic " + topic + " is registered with Router.Discard in this consumer")
	}))
}

// Topics implements worker.TopicOwner: the union of every registered rule plus
// every dynamic owner's current set — exactly what a Drain through this Router
// may claim, recomputed per Drain so runtime changes take effect immediately.
func (r *Router) Topics() worker.TopicSet {
	set := r.topics
	for _, o := range r.owners {
		set = set.Merge(o.Topics())
	}
	return set
}

// Labels returns the registered rule labels, for logs and errors.
func (r *Router) Labels() []string {
	out := make([]string, len(r.routes))
	for i, e := range r.routes {
		out[i] = e.label
	}
	return out
}

// Process implements worker.Processor: dispatch to the first matching consumer.
// An unmatched topic FAILS LOUDLY — the row keeps retrying and finally parks in
// state='failed' with this message as its last_error, where /admin/outbox and the
// failed-rows alert surface it. It is never acknowledged.
func (r *Router) Process(ctx context.Context, row worker.Row) error {
	for _, e := range r.routes {
		if e.match(row.Topic) {
			return e.proc.Process(ctx, row)
		}
	}
	return fmt.Errorf("router: no consumer registered for topic %q (registered: %s) — refusing to acknowledge; register a consumer for it, or Discard it explicitly",
		row.Topic, strings.Join(r.Labels(), ", "))
}
