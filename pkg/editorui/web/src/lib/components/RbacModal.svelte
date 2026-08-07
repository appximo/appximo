<script lang="ts">
	import { editor } from '../stores/editor.svelte';
	import { ui } from '../stores/ui.svelte';
	import { RBAC_ACTIONS } from '../types/schema';
	import type { Condition, ResourcePermission, RolePolicy } from '../types/schema';

	const CONCRETE = ['read', 'create', 'update', 'delete'] as const;

	let active = $state<string | null>(null);
	let newRole = $state('');
	let addError = $state<string | null>(null);
	let renameError = $state<string | null>(null);

	const roles = $derived(editor.roleNames);
	const role = $derived(active ? editor.getRole(active) : undefined);
	const isPer = $derived(active ? editor.roleForm(active) === 'perResource' : false);
	const roleIssues = $derived(
		active ? editor.rbacIssues().filter((i) => i.startsWith(`role "${active}"`)) : []
	);

	// Auto-select the first role when the panel opens with none active.
	$effect(() => {
		if (ui.rbacOpen && (!active || !editor.getRole(active))) {
			active = editor.roleNames[0] ?? null;
		}
	});

	function addRole() {
		addError = editor.addRole(newRole);
		if (!addError) {
			active = newRole.trim();
			newRole = '';
		}
	}
	function rename(e: Event) {
		if (!active) return;
		const v = (e.currentTarget as HTMLInputElement).value;
		renameError = editor.renameRole(active, v);
		if (!renameError) active = v.trim();
	}
	function removeRole() {
		if (!active) return;
		editor.deleteRole(active);
		active = editor.roleNames[0] ?? null;
	}

	// ── leaf editors (mutate the reactive policy in place; touch() bumps revision) ──
	type ActionsCarrier = { actions?: string[] };
	type CondCarrier = { conditions?: Condition; condition_actions?: string[] };
	type FieldsCarrier = { fields?: string[] };

	function actionChecked(c: ActionsCarrier, a: string) {
		return !!c.actions && (c.actions.includes(a) || c.actions.includes('*'));
	}
	function setAction(c: ActionsCarrier, a: string, on: boolean) {
		const cur = new Set(c.actions?.includes('*') ? [...CONCRETE] : (c.actions ?? []));
		if (on) cur.add(a);
		else cur.delete(a);
		c.actions = CONCRETE.filter((x) => cur.has(x));
		editor.touch();
	}

	function setCondField(c: CondCarrier, field: string) {
		if (!field) {
			delete c.conditions;
			delete c.condition_actions;
		} else if (c.conditions) {
			c.conditions.field = field;
		} else {
			c.conditions = { field, op: 'eq', val: '$user_id' };
		}
		editor.touch();
	}
	function valMode(c: CondCarrier): 'user' | 'client' | 'literal' {
		const v = c.conditions?.val;
		return v === '$user_id' ? 'user' : v === '$external_client_id' ? 'client' : 'literal';
	}
	function setValMode(c: CondCarrier, mode: string) {
		if (!c.conditions) return;
		if (mode === 'user') c.conditions.val = '$user_id';
		else if (mode === 'client') c.conditions.val = '$external_client_id';
		else c.conditions.val = c.conditions.val?.startsWith('$') ? '' : (c.conditions.val ?? '');
		editor.touch();
	}
	function setLiteral(c: CondCarrier, v: string) {
		if (c.conditions) {
			c.conditions.val = v;
			editor.touch();
		}
	}

	function condActionChecked(p: ResourcePermission, a: string) {
		return !!p.condition_actions?.includes(a);
	}
	function setCondAction(p: ResourcePermission, a: string, on: boolean) {
		const cur = new Set(p.condition_actions ?? []);
		if (on) cur.add(a);
		else cur.delete(a);
		const arr = CONCRETE.filter((x) => cur.has(x));
		if (arr.length > 0) p.condition_actions = arr;
		else delete p.condition_actions;
		editor.touch();
	}

	function fieldAllowed(c: FieldsCarrier, f: string) {
		return !!c.fields?.includes(f);
	}
	function setFieldAllowed(c: FieldsCarrier, f: string, on: boolean) {
		const cur = new Set(c.fields ?? []);
		if (on) cur.add(f);
		else cur.delete(f);
		const arr = [...cur];
		if (arr.length > 0) c.fields = arr;
		else delete c.fields;
		editor.touch();
	}

	// role-global resources
	function setWildcard(r: RolePolicy, on: boolean) {
		r.resources = on ? '*' : [];
		editor.touch();
	}
	function resChecked(r: RolePolicy, name: string) {
		return Array.isArray(r.resources) && r.resources.includes(name);
	}
	function toggleRes(r: RolePolicy, name: string, on: boolean) {
		const cur = new Set(Array.isArray(r.resources) ? r.resources : []);
		if (on) cur.add(name);
		else cur.delete(name);
		// rbacResourceNames keeps the virtual `files` store — filtering through
		// entityNames silently dropped a legal grant (ST2).
		r.resources = editor.rbacResourceNames.filter((n) => cur.has(n));
		editor.touch();
	}

	const grantedResources = $derived(role?.permissions ? Object.keys(role.permissions) : []);
	const addableResources = $derived(editor.rbacResourceNames.filter((n) => !grantedResources.includes(n)));
	let addRes = $state('');
	function doAddPermission() {
		if (active && addRes) {
			editor.addPermission(active, addRes);
			addRes = '';
		}
	}
</script>

{#snippet actionBoxes(carrier: ActionsCarrier)}
	<div class="acts">
		{#each CONCRETE as a}
			<label class="chk">
				<input
					type="checkbox"
					checked={actionChecked(carrier, a)}
					onchange={(e) => setAction(carrier, a, e.currentTarget.checked)}
				/>
				{a}
			</label>
		{/each}
		{#if carrier.actions?.includes('*')}<span class="star-note">grants all (*)</span>{/if}
	</div>
{/snippet}

{#snippet conditionEditor(carrier: CondCarrier, fieldOpts: string[], perm: ResourcePermission | null)}
	<div class="cond">
		<div class="cond-row">
			<span class="cond-lead">row filter:</span>
			<select
				class="field-select sm"
				value={carrier.conditions?.field ?? ''}
				onchange={(e) => setCondField(carrier, e.currentTarget.value)}
			>
				<option value="">— no filter (all rows) —</option>
				{#each fieldOpts as f}<option value={f}>{f}</option>{/each}
			</select>
			{#if carrier.conditions?.field}
				<span class="op-eq" title="row conditions are equality-only (engine-enforced)">=</span>
				<select
					class="field-select sm"
					value={valMode(carrier)}
					onchange={(e) => setValMode(carrier, e.currentTarget.value)}
				>
					<option value="user">$user_id</option>
					<option value="client">$external_client_id</option>
					<option value="literal">literal…</option>
				</select>
				{#if valMode(carrier) === 'literal'}
					<input
						class="field-input sm"
						placeholder="literal value"
						value={carrier.conditions.val}
						oninput={(e) => setLiteral(carrier, e.currentTarget.value)}
					/>
				{/if}
			{/if}
		</div>
		{#if carrier.conditions?.field && perm}
			<div class="cond-actions">
				<span class="cond-lead">filter applies to:</span>
				{#each CONCRETE as a}
					{#if actionChecked(perm, a)}
						<label class="chk sm">
							<input
								type="checkbox"
								checked={condActionChecked(perm, a)}
								onchange={(e) => setCondAction(perm, a, e.currentTarget.checked)}
							/>
							{a}
						</label>
					{/if}
				{/each}
				<span class="muted xs">none = all granted actions</span>
			</div>
		{/if}
	</div>
{/snippet}

{#snippet fieldAllowlist(carrier: FieldsCarrier, fieldOpts: string[])}
	<div class="allow">
		<span class="cond-lead">visible fields:</span>
		<div class="allow-grid">
			{#each fieldOpts as f}
				<label class="chk sm">
					<input
						type="checkbox"
						checked={fieldAllowed(carrier, f)}
						onchange={(e) => setFieldAllowed(carrier, f, e.currentTarget.checked)}
					/>
					{f}
				</label>
			{/each}
		</div>
		<span class="muted xs">none checked = all fields visible</span>
	</div>
{/snippet}

{#if ui.rbacOpen}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="overlay"
		onclick={(e) => {
			if (e.target === e.currentTarget) ui.rbacOpen = false;
		}}
		onkeydown={(e) => {
			if (e.key === 'Escape') ui.rbacOpen = false;
		}}
		role="presentation"
	>
		<div class="modal" role="dialog" aria-modal="true" aria-label="Roles and permissions" tabindex="-1">
			<div class="m-head">
				<div class="m-title"><span class="m-logo">🛡</span> Roles &amp; Permissions</div>
				<button class="btn subtle xs" onclick={() => (ui.rbacOpen = false)} aria-label="Close">✕</button>
			</div>

			<div class="m-body">
				<!-- left: role list -->
				<aside class="roles">
					{#each roles as r (r)}
						<button class="role-item" class:on={active === r} onclick={() => (active = r)}>
							<span class="ri-name">{r}</span>
							<span class="ri-form">{editor.roleForm(r) === 'perResource' ? 'per-resource' : 'global'}</span>
						</button>
					{/each}
					{#if roles.length === 0}<div class="muted pad">no roles yet</div>{/if}
					<div class="add-role">
						<input
							class="field-input sm"
							placeholder="new role name"
							bind:value={newRole}
							onkeydown={(e) => e.key === 'Enter' && addRole()}
						/>
						<button class="btn sm" onclick={addRole}>+ Add</button>
					</div>
					{#if addError}<div class="err xs">{addError}</div>{/if}
				</aside>

				<!-- right: role editor -->
				<section class="editor">
					{#if !active || !role}
						<div class="empty">Select or add a role to edit its permissions.</div>
					{:else}
						<div class="role-head">
							<input class="field-input role-name" value={active} onchange={rename} spellcheck="false" />
							<button class="btn danger sm" onclick={removeRole}>Delete role</button>
						</div>
						{#if renameError}<div class="err xs">{renameError}</div>{/if}

						{#if roleIssues.length > 0}
							<div class="banner err">
								<ul>{#each roleIssues as i}<li>{i}</li>{/each}</ul>
							</div>
						{/if}

						{#if isPer}
							<!-- ── PER-RESOURCE FORM ── -->
							<div class="form-tag">Per-resource permissions</div>
							{#each grantedResources as res (res)}
								{@const perm = role.permissions![res]}
								{@const virtual = editor.isVirtualResource(res)}
								<div class="grant">
									<div class="grant-head">
										<span class="grant-res">{res}{#if virtual}<span class="muted"> (built-in file store)</span>{/if}</span>
										<button class="btn subtle xs" onclick={() => editor.removePermission(active!, res)}>remove</button>
									</div>
									<div class="grant-row"><span class="grow-lbl">can</span>{@render actionBoxes(perm)}</div>
									{#if virtual}
										<div class="grant-row muted">actions only — the engine's file store has no row conditions or field allowlists</div>
									{:else}
										<div class="grant-row">{@render conditionEditor(perm, editor.fieldNamesForResource(res), perm)}</div>
										<div class="grant-row">{@render fieldAllowlist(perm, editor.fieldNamesForResource(res))}</div>
									{/if}
								</div>
							{/each}
							{#if grantedResources.length === 0}
								<div class="muted pad">No resources granted — this role can do nothing (deny-by-default).</div>
							{/if}
							{#if addableResources.length > 0}
								<div class="add-grant">
									<select class="field-select sm" bind:value={addRes}>
										<option value="">+ add resource…</option>
										{#each addableResources as n}<option value={n}>{n}</option>{/each}
									</select>
									<button class="btn sm" onclick={doAddPermission} disabled={!addRes}>Add</button>
								</div>
							{/if}
						{:else}
							<!-- ── ROLE-GLOBAL (legacy) FORM ── -->
							<div class="form-tag">
								Role-global
								<button class="btn subtle xs" onclick={() => editor.convertToPerResource(active!)}>
									convert to per-resource ▸
								</button>
							</div>
							<div class="grant">
								<div class="grant-row">
									<span class="grow-lbl">resources</span>
									<label class="chk"><input type="checkbox" checked={role.resources === '*'} onchange={(e) => setWildcard(role, e.currentTarget.checked)} /> all (*)</label>
								</div>
								{#if role.resources !== '*'}
									<div class="grant-row res-grid">
										{#each editor.rbacResourceNames as n}
											<label class="chk sm"><input type="checkbox" checked={resChecked(role, n)} onchange={(e) => toggleRes(role, n, e.currentTarget.checked)} /> {n}{#if editor.isVirtualResource(n)}<span class="muted"> ⬡</span>{/if}</label>
										{/each}
									</div>
								{/if}
								<div class="grant-row"><span class="grow-lbl">can</span>{@render actionBoxes(role)}</div>
								<div class="grant-row">{@render conditionEditor(role, editor.rbacFieldUnion(editor.roleResourceNames(role)), null)}</div>
								<div class="grant-row">{@render fieldAllowlist(role, editor.rbacFieldUnion(editor.roleResourceNames(role)))}</div>
							</div>
						{/if}

						<div class="deny-note">Deny-by-default: anything not granted above is denied.</div>
					{/if}
				</section>
			</div>

			<div class="m-foot">
				<span class="foot-hint">Edits apply to the schema instantly — Deploy to enforce them on the live API.</span>
				<button class="btn primary" onclick={() => (ui.rbacOpen = false)}>Done</button>
			</div>
		</div>
	</div>
{/if}

<style>
	.overlay {
		position: fixed;
		inset: 0;
		background: color-mix(in srgb, #0a0c10 46%, transparent);
		backdrop-filter: blur(3px);
		display: grid;
		place-items: center;
		z-index: 200;
	}
	.modal {
		width: min(900px, 95vw);
		height: min(680px, 90vh);
		display: flex;
		flex-direction: column;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow-lg);
		overflow: hidden;
	}
	.m-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 12px 16px;
		border-bottom: 1px solid var(--border);
	}
	.m-title {
		display: flex;
		align-items: center;
		gap: 8px;
		font-weight: 700;
	}
	.m-logo {
		font-size: 15px;
	}
	.btn.xs {
		padding: 3px 7px;
		font-size: 12px;
	}
	.btn.sm {
		padding: 4px 9px;
		font-size: 12px;
	}

	.m-body {
		flex: 1;
		display: flex;
		min-height: 0;
	}
	.roles {
		width: 210px;
		flex: 0 0 210px;
		border-right: 1px solid var(--border);
		padding: 10px;
		display: flex;
		flex-direction: column;
		gap: 3px;
		overflow-y: auto;
	}
	.role-item {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 1px;
		text-align: left;
		padding: 7px 10px;
		border: 1px solid transparent;
		border-radius: var(--radius-sm);
		background: var(--surface-2);
	}
	.role-item:hover {
		border-color: var(--border-strong);
	}
	.role-item.on {
		border-color: var(--brand);
		background: var(--brand-100);
	}
	.ri-name {
		font-weight: 600;
		font-family: var(--mono);
		font-size: 12.5px;
	}
	.ri-form {
		font-size: 10px;
		color: var(--text-3);
	}
	.add-role {
		display: flex;
		gap: 4px;
		margin-top: 8px;
	}

	.editor {
		flex: 1;
		padding: 14px 16px;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 10px;
	}
	.empty {
		color: var(--text-3);
		display: grid;
		place-items: center;
		height: 100%;
	}
	.role-head {
		display: flex;
		gap: 10px;
		align-items: center;
	}
	.role-name {
		font-family: var(--mono);
		font-weight: 700;
		font-size: 15px;
		flex: 1;
	}
	.form-tag {
		display: flex;
		align-items: center;
		gap: 10px;
		font-size: 11px;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-3);
		font-weight: 700;
	}

	.grant {
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		padding: 10px 12px;
		display: flex;
		flex-direction: column;
		gap: 9px;
		background: var(--surface);
	}
	.grant-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}
	.grant-res {
		font-family: var(--mono);
		font-weight: 700;
		color: var(--accent-fk);
	}
	.grant-row {
		display: flex;
		align-items: flex-start;
		gap: 8px;
		flex-wrap: wrap;
	}
	.grow-lbl {
		font-size: 11px;
		font-weight: 600;
		color: var(--text-2);
		min-width: 64px;
		padding-top: 3px;
	}
	.acts {
		display: flex;
		gap: 12px;
		flex-wrap: wrap;
		align-items: center;
	}
	.chk {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		font-size: 12.5px;
	}
	.chk.sm {
		font-size: 12px;
	}
	.star-note {
		font-size: 11px;
		color: var(--violet);
		font-weight: 600;
	}
	.cond,
	.allow {
		display: flex;
		flex-direction: column;
		gap: 6px;
		width: 100%;
	}
	.cond-row,
	.cond-actions {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
	}
	.cond-lead {
		font-size: 11px;
		font-weight: 600;
		color: var(--text-2);
	}
	.op-eq {
		font-family: var(--mono);
		font-weight: 700;
		color: var(--text-3);
	}
	.field-select.sm,
	.field-input.sm {
		width: auto;
		padding: 4px 7px;
		font-size: 12px;
	}
	.allow-grid,
	.res-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(110px, 1fr));
		gap: 3px 10px;
		flex: 1;
	}
	.muted {
		color: var(--text-3);
	}
	.xs {
		font-size: 11px;
	}
	.pad {
		padding: 10px 0;
	}
	.add-grant,
	.add-role {
		display: flex;
		gap: 6px;
		align-items: center;
	}
	.deny-note {
		font-size: 11.5px;
		color: var(--text-3);
		font-style: italic;
		margin-top: 2px;
	}
	.err {
		color: var(--danger);
	}
	.banner.err {
		background: color-mix(in srgb, var(--danger) 10%, transparent);
		border: 1px solid color-mix(in srgb, var(--danger) 40%, transparent);
		border-radius: var(--radius-sm);
		padding: 8px 12px;
		color: var(--danger);
		font-size: 12px;
	}
	.banner.err ul {
		margin: 0;
		padding-left: 16px;
	}

	.m-foot {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 11px 16px;
		border-top: 1px solid var(--border);
	}
	.foot-hint {
		font-size: 11.5px;
		color: var(--text-3);
	}
</style>
