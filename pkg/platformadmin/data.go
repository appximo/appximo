package platformadmin

import (
	"context"
	"errors"
	"net/url"
	"sort"

	"github.com/appximo/appximo/pkg/db"
	pkghandlers "github.com/appximo/appximo/pkg/handlers"
	"github.com/appximo/appximo/pkg/query"
	"github.com/appximo/appximo/pkg/schema"
)

// ErrResourceNotFound is returned when a tenant has no such resource (or no schema).
var ErrResourceNotFound = errors.New("platformadmin: resource not found")

// SetTenantDB wires the engine's tenant-scoped DB so the admin API can browse a
// tenant's DATA (read-only). It is set by app.go after NewService. When unset, the
// data-browsing endpoints return an error (the CLI/bootstrap path never needs it).
func (s *Service) SetTenantDB(tdb *db.TenantDB) { s.tdb = tdb }

// ResourceField is one column descriptor (name + declared type) for the data UI.
type ResourceField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// ResourceInfo is a tenant resource the operator can browse.
type ResourceInfo struct {
	Name   string          `json:"name"`
	Fields []ResourceField `json:"fields"`
}

// ListResources returns the tenant's resources (from its STORED schema — the
// engine already knows them; this does not re-derive anything) with each field's
// declared type, so the data UI can render typed columns. The implicit `id` UUID
// PK is listed first.
func (s *Service) ListResources(ctx context.Context, tenantID string) ([]ResourceInfo, error) {
	if !tenantIDRe.MatchString(tenantID) {
		return nil, ErrTenantNotFound
	}
	sc, err := s.cp.GetSchema(ctx, tenantID)
	if err != nil {
		return nil, err // controlplane.ErrNotFound mapped by the handler
	}
	if sc == nil {
		return []ResourceInfo{}, nil
	}
	out := make([]ResourceInfo, 0, len(sc.Resources))
	for name, res := range sc.Resources {
		names := make([]string, 0, len(res.Fields))
		for fn := range res.Fields {
			names = append(names, fn)
		}
		sort.Strings(names)
		fields := make([]ResourceField, 0, len(names)+1)
		fields = append(fields, ResourceField{Name: "id", Type: "uuid"}) // implicit PK, first
		for _, fn := range names {
			fields = append(fields, ResourceField{Name: fn, Type: res.Fields[fn].Type})
		}
		out = append(out, ResourceInfo{Name: name, Fields: fields})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ListRoles returns the RBAC role names declared in the tenant's stored schema,
// so the user-management UI can offer a role picker (it only ASSIGNS existing
// roles — creating roles is the schema editor, out of scope). Sorted.
func (s *Service) ListRoles(ctx context.Context, tenantID string) ([]string, error) {
	if !tenantIDRe.MatchString(tenantID) {
		return nil, ErrTenantNotFound
	}
	sc, err := s.cp.GetSchema(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if sc == nil {
		return []string{}, nil
	}
	out := make([]string, 0, len(sc.RBAC.Roles))
	for r := range sc.RBAC.Roles {
		out = append(out, r)
	}
	sort.Strings(out)
	return out, nil
}

// ListData returns a page of a tenant resource's records (READ-ONLY browse). It
// REUSES the engine's validated, injection-safe query builder (filters/sort/keyset
// pagination from `params`) and runs it through the tenant-scoped DB — no new query
// logic, and the tenant_<id> search_path is the isolation boundary. Authorization
// is the platform super-admin (any tenant); a record's columns are whatever the
// resource declares. `meta.has_next` uses the keyset cursor convention (fetch
// per_page; the UI loads more via ?after=<last id>).
func (s *Service) ListData(ctx context.Context, tenantID, resource string, params url.Values) (map[string]any, error) {
	if s.tdb == nil {
		return nil, errors.New("platformadmin: data browsing not configured")
	}
	if !tenantIDRe.MatchString(tenantID) {
		return nil, ErrTenantNotFound
	}
	sc, err := s.cp.GetSchema(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if sc == nil {
		return nil, ErrResourceNotFound
	}
	res, ok := sc.Resources[resource]
	if !ok {
		return nil, ErrResourceNotFound
	}
	qb, err := query.BuildQuery(resource, &res, params, nil, nil) // nil cond/allowlist: super-admin sees all rows and fields
	if err != nil {
		return nil, err // a bad filter/sort param → 400 (mapped by handler)
	}
	selectQ, _, selectArgs, _ := qb.SQL()
	rows, err := s.tdb.QueryTenant(ctx, "tenant_"+tenantID, selectQ, selectArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, err := pkghandlers.RowsToMaps(rows)
	if err != nil {
		return nil, err
	}
	schema.PromoteJSONTextRows(out, res.JSONTextColumns()) // ADR-028: a json column is a value here too
	return map[string]any{
		"data": out,
		"meta": map[string]any{
			"page":     qb.Page(),
			"per_page": qb.PerPage(),
			"count":    len(out),
			"has_next": len(out) >= qb.PerPage(),
		},
	}, nil
}
