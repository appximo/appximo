// Types for the deploy flow — faithful mirrors of the engine/admin-API JSON shapes
// (pkg/migration.Preview / DestructiveOp, pkg/platformadmin.TenantInfo). These are
// the contract for talking to /admin/*.

/** One data-losing drop in a migration preview (pkg/migration.DestructiveOp). */
export interface DestructiveOp {
	key: string; // approval token: "<table>" or "<table>.<column>"
	kind: 'table' | 'column';
	table: string;
	column: string; // "" for a table drop
	rows_lost: number; // rows whose data is destroyed
	table_rows: number; // total rows (context)
	approved: boolean; // approval status in the previewed set
	summary: string; // human one-liner with the impact
}

/** The classified dry-run plan (pkg/migration.Preview). */
export interface Preview {
	pg_schema: string;
	empty?: boolean; // already converged — nothing to do
	apply?: string[]; // safe ops that will run
	destructive?: DestructiveOp[]; // data-losing drops (gated unless approved)
	drift?: string[]; // safe drops left as additive drift
	concerns?: string[]; // backfill/type-change risks on existing data
	unmatched_approvals?: string[];
}

export interface PreviewResponse {
	status: 'dry_run';
	preview: Preview;
}

export interface ApplyResponse {
	status: 'applied';
	tenant_id: string;
	applied_drops?: string[];
	gated_drops?: string[];
	unmatched_approvals?: string[];
}

/** A registered tenant summary (pkg/platformadmin.TenantInfo). */
export interface TenantInfo {
	id: string;
	display_name: string;
	email: string;
	plan: string;
	suspended: boolean;
	created_at: string;
	resource_count: number;
}

export interface AdminUser {
	id: string;
	email: string;
	role: string;
}

/** POST /admin/auth/login result (pkg/platformadmin.PlatformAuthResult). */
export interface LoginResult {
	admin?: AdminUser;
	token?: string;
	mfa_required?: boolean;
	mfa_token?: string;
}

/** POST /admin/tenants result (controlplane.Tenant). */
export interface CreatedTenant {
	id: string;
	pg_schema: string;
	display_name: string;
	email: string;
	plan: string;
	created_at: string;
	/** Where this tenant will actually answer: <id>.<domain> (ENG-11). */
	reachable_at?: string;
	/** Set when the id does not match the address this app is served at. */
	warning?: string;
}
