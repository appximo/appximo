package codegen

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/appximo/appximo/pkg/db"
	pkghandlers "github.com/appximo/appximo/pkg/handlers"
	"github.com/appximo/appximo/pkg/query"
	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/summary"
	"github.com/appximo/appximo/pkg/tenant"
)

// registerSummaryRoute mounts GET /api/summary (VOZ-ESCALON1-S1): a read-only,
// cross-resource daily digest in the OWNER'S language — "what happened today"
// per resource — composed deterministically from data the engine already
// computes. It is the first rung of the voice plan (A-70): the same digest the
// Telegram receiver returns for "resumen" and the cron workflow sends each
// morning.
//
// Since VOZ-VISUAL-S1 it also answers as an IMAGE — `?format=png` (the ONLY
// door: a separate URL, so the response cache keeps the two representations
// apart) — rendered on the server from the SAME counts
// (pkg/summary/render.go), so the picture can never say something the text
// does not. The schema's top-level `summary.resources` block chooses which
// resources enter and in what order (absent ⇒ every readable resource,
// attention first), and `state_machine.pending` decides what "waiting" means.
//
// It is NOT a resource — like /api/transaction it is a reserved segment the
// RBAC middleware passes through (schema.reservedSummaryResource), and this
// handler authorizes EACH resource itself: only the resources the caller's role
// may read are included, each scoped by that role's row condition and field
// allowlist (a role that cannot read a resource never sees it in the digest; a
// role scoped to its own rows counts only its own). So the digest can never leak
// what a plain list would not.
func registerSummaryRoute(r chi.Router, s *schema.APISchema, tdb *db.TenantDB, policy *rbac.Policy) {
	// The resources the digest asks about: the declared list (its order), else
	// every resource (alphabetical; Order re-ranks by attention afterwards).
	// A declared list also means FEWER queries — an app with twenty resources
	// that names four pays for four.
	var names []string
	if s.Summary != nil && len(s.Summary.Resources) > 0 {
		names = append(names, s.Summary.Resources...)
	} else {
		for name := range s.Resources {
			names = append(names, name)
		}
		sort.Strings(names)
	}

	// Per-resource digest plan, computed once at boot from the schema.
	plans := make(map[string]summary.Plan, len(names))
	for _, name := range names {
		res := s.Resources[name]
		plans[name] = summary.PlanFor(name, &res)
	}

	r.Get("/api/summary", pkghandlers.CachedGet(func(w http.ResponseWriter, req *http.Request) {
		tc := tenant.MustFromCtx(req.Context())
		evalCtx := rbac.EvalContextFromRequest(req)

		// /api/summary is a reserved pass-through (the RBAC middleware injects no
		// EvalResult), so this handler is the deny point. An ANONYMOUS caller —
		// no JWT claims and no identity — is forbidden, exactly like deny-by-
		// default on any /api/ resource (an empty 200 digest for a stranger
		// would be a silent hole; an authenticated role that simply reads
		// nothing still gets a 200 "sin movimiento"). See VOZ-ESCALON1-S1.
		if evalCtx.Role == "" && evalCtx.UserID == "" && evalCtx.ExternalClientID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"}) //nolint:errcheck
			return
		}

		// The report's day is the engine's local day. A tenant timezone is a
		// future refinement (the cron workflow already declares its own zone);
		// the digest labels the day it was computed.
		now := time.Now()
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		startISO := startOfDay.UTC().Format(time.RFC3339)

		q := req.URL.Query()
		census := q.Get("view") == "census"
		// ONLY ?format=png selects the image. An Accept-header door shared the
		// URL with the JSON and the response cache keys by URL: a cached JSON
		// answered an image request (and could have done the reverse) — seen
		// live on the 58. Two representations, two URLs, two cache entries.
		wantPNG := q.Get("format") == "png"
		// ?mode=scheduled (VOZ-DELTA-S1): the morning run. The digest is
		// compared against the baseline like any other call, but this one also
		// APPLIES the send policy (summary.notify / quiet_days) and records its
		// decision on today's snapshot, so the caller (the worker's consumer)
		// only has to obey should_send — and a quiet channel stays provable.
		scheduled := q.Get("mode") == "scheduled"
		day := startOfDay.Format("2006-01-02")
		role := evalCtx.Role
		pool := tdb.Pool()

		facts := make([]summary.Facts, 0, len(names))
		for _, name := range names {
			// Per-resource read authorization (the middleware passed /api/summary
			// through, so we evaluate here — same policy, same principal).
			ev := policy.Evaluate(evalCtx, name, "read")
			if !ev.Allowed {
				continue
			}
			res := s.Resources[name]
			p := plans[name]
			f := summary.Facts{Resource: name}

			scopedCount := func(params url.Values) (int64, bool) {
				aq, err := query.BuildAggregate(name, readSurface(req.Context(), tc.ID, name, &res), params, ev.Condition, ev.AllowedFields)
				if err != nil {
					// A field the role may not read (ErrAggForbiddenField) or any
					// build error → the digest stays silent about that metric,
					// never an error to the caller.
					return 0, false
				}
				sqlStr, args := aq.SQL()
				rows, err := tdb.QueryDirect(req.Context(), tc.PGSchema, name, sqlStr, args...)
				if err != nil {
					return 0, false
				}
				defer rows.Close()
				recs, err := pkghandlers.RowsToMaps(rows)
				if err != nil || len(recs) == 0 {
					return 0, false
				}
				return toInt64(recs[0][query.CountAlias]), true
			}

			if census {
				if n, ok := scopedCount(url.Values{"count": {"true"}}); ok {
					f.Total, f.HasTotal = n, true
				}
				facts = append(facts, f)
				continue
			}

			if p.CreatedTsField != "" {
				params := url.Values{"count": {"true"}}
				params.Set("filter["+p.CreatedTsField+"][gte]", startISO)
				if n, ok := scopedCount(params); ok {
					f.CreatedToday, f.HasCreated = n, true
				}
			}
			if p.UpdatedTsField != "" {
				params := url.Values{"count": {"true"}}
				params.Set("filter["+p.UpdatedTsField+"][gte]", startISO)
				if n, ok := scopedCount(params); ok {
					f.UpdatedToday, f.HasUpdated = n, true
				}
			}
			if p.StateField != "" {
				countStates := func(states []string) (map[string]int64, int64, bool) {
					out := map[string]int64{}
					var total int64
					got := false
					for _, st := range states {
						params := url.Values{"count": {"true"}}
						params.Set("filter["+p.StateField+"][eq]", st)
						if n, ok := scopedCount(params); ok {
							got = true
							if n > 0 {
								out[st] = n
								total += n
							}
						}
					}
					return out, total, got
				}
				if m, total, got := countStates(p.Attention); got {
					f.HasState = true
					f.Attention, f.AttentionTotal, f.AttentionInferred = m, total, p.AttentionInferred
					// What ARRIVED today among the waiting rows — the news, as
					// opposed to the stock that has waited for months.
					if p.CreatedTsField != "" && total > 0 {
						var newToday int64
						gotNew := false
						for _, st := range p.Attention {
							params := url.Values{"count": {"true"}}
							params.Set("filter["+p.StateField+"][eq]", st)
							params.Set("filter["+p.CreatedTsField+"][gte]", startISO)
							if n, ok := scopedCount(params); ok {
								gotNew = true
								newToday += n
							}
						}
						f.NewToday, f.HasNewToday = newToday, gotNew
					}
				}
				if m, total, got := countStates(p.Flow); got {
					f.HasState = true
					f.Flow, f.FlowTotal = m, total
				}
			}
			facts = append(facts, f)
		}

		appName := os.Getenv("APPXIMO_ALERT_APP_NAME")
		if appName == "" {
			appName = s.Name
		}

		ordered := summary.Order(s.Summary, facts)
		var rep summary.Report
		if census {
			// The census carries the last scheduled evaluation (today's row if
			// it ran today, else the baseline's) so `estado` proves the automatic
			// send is alive even when it chose to stay silent.
			last, _ := summary.LoadDay(req.Context(), pool, tc.ID, role, day)
			if last == nil || last.ScheduledAt == nil {
				if b, _ := summary.LoadBaseline(req.Context(), pool, tc.ID, role, day); b != nil && b.ScheduledAt != nil {
					last = b
				}
			}
			rep = summary.ComposeCensus(appName, tc.ID, ordered, last)
		} else {
			// The delta: compare against the most recent snapshot from a previous
			// day, then remember today (one row per day; older rows pruned).
			base, berr := summary.LoadBaseline(req.Context(), pool, tc.ID, role, day)
			if berr != nil {
				// A snapshot problem must never take the digest down: no
				// comparison is "first summary", said plainly, never a fake zero.
				base = nil
			}
			summary.ApplyBaseline(ordered, base)
			rep = summary.Compose(appName, tc.ID, day, ordered, base)
			snap := summary.SnapshotOf(tc.ID, role, day, rep.Level, ordered)
			baselineDay := ""
			prevStreak := 0
			if base != nil {
				baselineDay, prevStreak = base.Day, base.SilentStreak
			}
			if scheduled {
				summary.Decide(&rep, policyOf(s.Summary), prevStreak)
				snap.SilentStreak = rep.SilentStreak
				if rep.ShouldSend {
					snap.Decision = "sent:" + rep.SendReason
				} else {
					snap.Decision = "silent"
				}
			} else if today, _ := summary.LoadDay(req.Context(), pool, tc.ID, role, day); today != nil {
				snap.SilentStreak = today.SilentStreak
			}
			if berr == nil {
				if uerr := summary.Upsert(req.Context(), pool, snap, scheduled, baselineDay); uerr != nil {
					// Logged by the pool layer; the digest still answers.
					_ = uerr
				}
			}
		}
		markSpan(req, "query")

		if wantPNG {
			png, err := summary.Render(rep)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "summary image could not be rendered — the text digest (without ?format=png) still works"}) //nolint:errcheck
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("X-Summary-Level", rep.Level)
			serverTiming(w, req)
			w.WriteHeader(http.StatusOK)
			w.Write(png) //nolint:errcheck
			markSpan(req, "render")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		serverTiming(w, req)
		json.NewEncoder(w).Encode(rep) //nolint:errcheck
		markSpan(req, "serialize")
	}))
}

// policyOf maps the schema's summary block onto the send policy with defaults.
func policyOf(cfg *schema.SummaryConfig) summary.Policy {
	p := summary.DefaultPolicy
	if cfg == nil {
		return p
	}
	if cfg.Notify != "" {
		p.Notify = cfg.Notify
	}
	if cfg.QuietDays != nil {
		p.QuietDays = *cfg.QuietDays
	}
	return p
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int32:
		return int64(n)
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}
