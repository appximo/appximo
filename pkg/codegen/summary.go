package codegen

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
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
// It is NOT a resource — like /api/transaction it is a reserved segment the
// RBAC middleware passes through (schema.reservedSummaryResource), and this
// handler authorizes EACH resource itself: only the resources the caller's role
// may read are included, each scoped by that role's row condition and field
// allowlist (a role that cannot read a resource never sees it in the digest; a
// role scoped to its own rows counts only its own). So the digest can never leak
// what a plain list would not.
func registerSummaryRoute(r chi.Router, s *schema.APISchema, tdb *db.TenantDB, policy *rbac.Policy) {
	names := make([]string, 0, len(s.Resources))
	for name := range s.Resources {
		names = append(names, name)
	}
	sort.Strings(names)

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

		census := req.URL.Query().Get("view") == "census"

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
					f.CreatedToday = n // reuse the field to carry the census total
					f.HasCreated = true
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
			if p.StateField != "" && len(p.PendingStates) > 0 {
				pending := map[string]int64{}
				var total int64
				got := false
				for _, st := range p.PendingStates {
					params := url.Values{"count": {"true"}}
					params.Set("filter["+p.StateField+"][eq]", st)
					if n, ok := scopedCount(params); ok {
						got = true
						if n > 0 {
							pending[st] = n
							total += n
						}
					}
				}
				if got {
					f.HasState, f.Pending, f.PendingTotal = true, pending, total
				}
			}
			facts = append(facts, f)
		}

		appName := os.Getenv("APPXIMO_ALERT_APP_NAME")
		if appName == "" {
			appName = s.Name
		}
		day := startOfDay.Format("2006-01-02")

		var rep summary.Report
		if census {
			rep = composeCensus(appName, tc.ID, facts)
		} else {
			rep = summary.Compose(appName, tc.ID, day, facts)
		}

		w.Header().Set("Content-Type", "application/json")
		serverTiming(w, req)
		markSpan(req, "query")
		json.NewEncoder(w).Encode(rep) //nolint:errcheck
		markSpan(req, "serialize")
	}))
}

// composeCensus renders the `estado` view: how big the business is right now
// (total rows per resource the role may read) — an owner census, not system
// metrics. Reuses Facts.CreatedToday as the carried total.
func composeCensus(appName, tenant string, facts []summary.Facts) summary.Report {
	sort.Slice(facts, func(i, j int) bool { return facts[i].Resource < facts[j].Resource })
	rep := summary.Report{AppName: appName, Tenant: tenant}
	var b strings.Builder
	fmt.Fprintf(&b, "📊 <b>Estado de %s</b>\n", htmlEscape(appName))
	any := false
	for _, f := range facts {
		if !f.HasCreated {
			continue
		}
		any = true
		fmt.Fprintf(&b, "• <b>%s</b>: %d\n", htmlEscape(f.Resource), f.CreatedToday)
	}
	if !any {
		rep.Text = "📊 <b>Estado de " + htmlEscape(appName) + "</b>\n\nNo hay datos todavía."
		return rep
	}
	rep.HasMotion = true
	rep.Text = strings.TrimRight(b.String(), "\n")
	return rep
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

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
