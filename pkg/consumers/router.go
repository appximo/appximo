package consumers

import (
	"context"
	"strings"

	"github.com/rs/zerolog"

	"github.com/miguelangel/appitools/pkg/worker"
)

// Router is the COEXISTENCE mechanism for a shared outbox carrying multiple event
// types. It is a worker.Processor that dispatches each row to the registered
// consumer whose topic rule matches, and acks (logs) anything unmatched.
//
// WHY a dispatcher and not one worker per mode: the outbox is one table drained by
// competing consumers under FOR UPDATE SKIP LOCKED, which distributes rows across
// ALL running workers regardless of topic. A single-purpose worker (xlsx-only,
// email-only) ACKS the topics it doesn't own — so if an xlsx worker and an email
// worker drain the SAME outbox, whichever claims a row first acks it, and the
// other consumer never sees it: silent event loss. The correct model for multiple
// event types is therefore ONE worker that handles ALL of them — a Router — scaled
// horizontally by running N identical Router workers (SKIP LOCKED still gives each
// a disjoint slice, no double-processing). Run single-mode workers only when they
// are the SOLE consumer of the outbox.
//
// Matching is by exact topic or by a "prefix.*" suffix wildcard (e.g.
// "filejobs.*"); the first registered rule that matches wins, so register more
// specific rules first.
type Router struct {
	routes []routerEntry
	log    zerolog.Logger
}

type routerEntry struct {
	match func(topic string) bool
	proc  worker.Processor
	label string
}

// NewRouter builds an empty Router. Add consumers with Handle / HandlePrefix.
func NewRouter(log zerolog.Logger) *Router { return &Router{log: log} }

// Handle routes the EXACT topic to proc. Returns r for chaining.
func (r *Router) Handle(topic string, proc worker.Processor) *Router {
	r.routes = append(r.routes, routerEntry{
		match: func(t string) bool { return t == topic },
		proc:  proc,
		label: topic,
	})
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
	return r
}

// Process implements worker.Processor: dispatch to the first matching consumer, or
// ack an unmatched topic (a Router IS the full set of consumers for its outbox, so
// an unknown topic is genuinely nothing to do — not another worker's job).
func (r *Router) Process(ctx context.Context, row worker.Row) error {
	for _, e := range r.routes {
		if e.match(row.Topic) {
			return e.proc.Process(ctx, row)
		}
	}
	r.log.Debug().Int64("id", row.ID).Str("topic", row.Topic).Msg("router: no consumer for topic, acking")
	return nil
}
