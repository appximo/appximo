<script lang="ts">
	import { editor } from '../stores/editor.svelte';
	import { ui } from '../stores/ui.svelte';
	import EntityDataPanel from './EntityDataPanel.svelte';
	import EntityRelationsPanel from './EntityRelationsPanel.svelte';
	import EntityHooksPanel from './EntityHooksPanel.svelte';
	import { smKnownStates } from '../schema/fieldRules';
	import {
		FIELD_TYPES,
		FIELD_FORMATS,
		REFERENTIAL_ACTIONS,
		EVENT_ACTIONS,
		type FieldType,
		type FieldFormat,
		type ReferentialAction,
		type EventAction
	} from '../types/schema';
	import { fieldDefIssues, isNumericType, isStringType } from '../schema/fieldRules';

	const entity = $derived(editor.selectedEntity);
	const field = $derived(editor.selectedField);

	let nameError = $state<string | null>(null);
	let fieldNameError = $state<string | null>(null);

	const isNumeric = $derived(field ? isNumericType(field.def.type) : false);
	const isStringy = $derived(field ? isStringType(field.def.type) : false);
	const isFile = $derived(field?.def.type === 'file');
	const hasEnum = $derived((field?.def.enum ?? []).length > 0);
	/** Live fidelity issues for the selected field — mirrors the engine validator. */
	const fieldIssues = $derived(field ? fieldDefIssues(field.def) : []);

	function renameEntity(value: string) {
		if (!entity) return;
		nameError = editor.renameEntity(entity.id, value);
	}
	function renameField(value: string) {
		if (!entity || !field) return;
		fieldNameError = editor.renameField(entity.id, field.id, value);
	}

	// def patch helpers -----------------------------------------------------------
	function setType(t: FieldType) {
		if (!entity || !field) return;
		editor.patchFieldDef(entity.id, field.id, 'type', t);
		// A file field's target is FIXED (the engine file store): the keys of an
		// authorable FK — and the ones the validator rejects on file — are cleared
		// so switching a field to `file` never carries dead/invalid config.
		if (t === 'file') {
			for (const k of ['relation', 'references', 'on_update', 'default', 'enum', 'auto'] as const) {
				editor.patchFieldDef(entity.id, field.id, k, undefined);
			}
			if (field.def.on_delete === 'cascade') {
				editor.patchFieldDef(entity.id, field.id, 'on_delete', undefined);
			}
		} else {
			// accept/max_bytes are file-field-only (FILES-1) — the validator rejects
			// them elsewhere, so switching AWAY from file must clear them.
			editor.patchFieldDef(entity.id, field.id, 'accept', undefined);
			editor.patchFieldDef(entity.id, field.id, 'max_bytes', undefined);
		}
	}
	// UI-1 (FILES-1): the file field's attach policy. `accept` round-trips the
	// engine's two shapes faithfully — a single value stays a STRING, several
	// become an array (both are valid; a JSON-declared policy survives edits).
	function acceptText(): string {
		const a = field?.def.accept;
		if (!a) return '';
		return Array.isArray(a) ? a.join(', ') : a;
	}
	function setAccept(raw: string) {
		if (!entity || !field) return;
		const arr = raw
			.split(',')
			.map((s) => s.trim())
			.filter(Boolean);
		const v = arr.length === 0 ? undefined : arr.length === 1 ? arr[0] : arr;
		editor.patchFieldDef(entity.id, field.id, 'accept', v);
	}
	function setMaxBytes(raw: string) {
		if (!entity || !field) return;
		const n = Number(raw);
		editor.patchFieldDef(
			entity.id,
			field.id,
			'max_bytes',
			raw.trim() === '' || Number.isNaN(n) || n <= 0 ? undefined : Math.floor(n)
		);
	}
	function toggleFlag(key: 'required' | 'unique', on: boolean) {
		if (entity && field) editor.patchFieldDef(entity.id, field.id, key, on ? true : undefined);
	}
	// auto is a union (true | "create" | "update") since SILENT-CORRUPTION-S1 —
	// a checkbox would silently degrade the string roles back to the legacy
	// boolean on toggle, which is exactly the corruption class the roles close.
	function setAuto(raw: string) {
		if (!entity || !field) return;
		const v = raw === '' ? undefined : raw === 'true' ? true : raw;
		editor.patchFieldDef(entity.id, field.id, 'auto', v);
	}
	function autoValue(): string {
		const a = field?.def.auto as unknown;
		if (a === true) return 'true';
		if (a === 'create' || a === 'update') return a;
		return '';
	}
	function setNum(key: 'min' | 'max' | 'minLength' | 'maxLength', raw: string) {
		if (!entity || !field) return;
		const v = raw.trim() === '' ? undefined : Number(raw);
		editor.patchFieldDef(entity.id, field.id, key, Number.isNaN(v as number) ? undefined : v);
	}
	function setStr(key: 'pattern', raw: string) {
		if (entity && field) editor.patchFieldDef(entity.id, field.id, key, raw.trim() || undefined);
	}
	function setFormat(raw: string) {
		if (entity && field)
			editor.patchFieldDef(entity.id, field.id, 'format', raw ? (raw as FieldFormat) : undefined);
	}
	function setEnum(raw: string) {
		if (!entity || !field) return;
		const arr = raw
			.split(',')
			.map((s) => s.trim())
			.filter(Boolean);
		editor.patchFieldDef(entity.id, field.id, 'enum', arr.length ? arr : undefined);
	}
	function setDefault(raw: string) {
		if (!entity || !field) return;
		if (raw.trim() === '') {
			editor.patchFieldDef(entity.id, field.id, 'default', undefined);
			return;
		}
		let val: unknown = raw;
		// An enum default must be a STRING member (the engine validates it as such),
		// so never coerce it to a number even on a numeric-typed enum field.
		if (hasEnum) {
			val = raw;
		} else if (isNumeric) {
			const n = Number(raw);
			if (!Number.isNaN(n)) val = n;
		} else if (field.def.type === 'bool') {
			val = raw === 'true';
		}
		editor.patchFieldDef(entity.id, field.id, 'default', val);
	}
	function setRelation(target: string) {
		if (!entity || !field) return;
		if (!target) {
			editor.patchFieldDef(entity.id, field.id, 'relation', undefined);
			editor.patchFieldDef(entity.id, field.id, 'on_delete', undefined);
			editor.patchFieldDef(entity.id, field.id, 'on_update', undefined);
			editor.patchFieldDef(entity.id, field.id, 'references', undefined);
			return;
		}
		editor.patchFieldDef(entity.id, field.id, 'type', 'uuid');
		editor.patchFieldDef(entity.id, field.id, 'relation', target);
		// A new target invalidates a non-id `references` — reset it to id (the default).
		editor.patchFieldDef(entity.id, field.id, 'references', undefined);
		if (!field.def.on_delete) editor.patchFieldDef(entity.id, field.id, 'on_delete', 'restrict');
	}
	function setOnDelete(v: string) {
		if (entity && field)
			editor.patchFieldDef(entity.id, field.id, 'on_delete', v ? (v as ReferentialAction) : undefined);
	}
	function setOnUpdate(v: string) {
		if (entity && field)
			editor.patchFieldDef(entity.id, field.id, 'on_update', v ? (v as ReferentialAction) : undefined);
	}
	function setReferences(v: string) {
		// "id" is the default — store it as absent so the round-trip stays minimal.
		if (entity && field)
			editor.patchFieldDef(entity.id, field.id, 'references', v && v !== 'id' ? v : undefined);
	}

	function toggleEvent(action: EventAction, on: boolean) {
		if (!entity) return;
		const cur = new Set(entity.extras.events ?? []);
		if (on) cur.add(action);
		else cur.delete(action);
		entity.extras.events = cur.size ? EVENT_ACTIONS.filter((a) => cur.has(a)) : undefined;
	}
</script>

<aside class="panel">
	{#if field && entity}
		<!-- ── FIELD inspector ─────────────────────────────────────────── -->
		<div class="p-head">
			<button class="crumb" onclick={() => editor.selectEntity(entity.id)}>{entity.name}</button>
			<span class="crumb-sep">/</span>
			<span class="crumb-cur">{field.name}</span>
		</div>

		<section class="p-sec">
			<label class="lbl" for="f-name">Field name</label>
			<input
				id="f-name"
				class="field-input"
				value={field.name}
				spellcheck="false"
				onchange={(e) => renameField(e.currentTarget.value)}
			/>
			{#if fieldNameError}<div class="err">{fieldNameError}</div>{/if}
			{#if field.originalName && field.originalName !== field.name}
				<div
					class="rename-note"
					title="The deploy runs ALTER TABLE … RENAME COLUMN: the column and its data move to the new name — nothing is dropped or recreated."
				>
					↪ renames “{field.originalName}” on deploy — existing data is preserved
				</div>
			{/if}

			<label class="lbl" for="f-type">Type</label>
			<select
				id="f-type"
				class="field-select"
				value={field.def.type}
				onchange={(e) => setType(e.currentTarget.value as FieldType)}
			>
				{#each FIELD_TYPES as t}<option value={t}>{t}</option>{/each}
			</select>

			<div class="flags">
				<label class="chk"
					><input
						type="checkbox"
						checked={!!field.def.required}
						onchange={(e) => toggleFlag('required', e.currentTarget.checked)}
					/> required</label
				>
				<label class="chk"
					><input
						type="checkbox"
						checked={!!field.def.unique}
						onchange={(e) => toggleFlag('unique', e.currentTarget.checked)}
					/> unique</label
				>
				<label class="chk auto-role" title="Engine-managed timestamp. create = set once at insert (any name). update = also refreshed by the engine on every update (any name). legacy true = create semantics, except the literal name updated_at which also refreshes."
					>auto
					<select
						class="field-select"
						disabled={isFile}
						value={autoValue()}
						onchange={(e) => setAuto(e.currentTarget.value)}
					>
						<option value="">off</option>
						<option value="create">create</option>
						<option value="update">update</option>
						<option value="true">legacy (true)</option>
					</select></label
				>
			</div>

			{#if isFile}
				<div class="rule-note muted">
					No default — a file field is set per record with a file_id from an upload.
				</div>
			{:else}
			<label class="lbl" for="f-default">Default <span class="muted">(on create)</span></label>
			{#if hasEnum}
				<!-- enum default: pick a member (the engine requires it to be one) -->
				<select
					id="f-default"
					class="field-select"
					value={field.def.default === undefined ? '' : String(field.def.default)}
					onchange={(e) => setDefault(e.currentTarget.value)}
				>
					<option value="">— none —</option>
					{#each field.def.enum ?? [] as ev}<option value={ev}>{ev}</option>{/each}
				</select>
			{:else if field.def.type === 'bool'}
				<select
					id="f-default"
					class="field-select"
					value={field.def.default === undefined ? '' : String(field.def.default)}
					onchange={(e) => setDefault(e.currentTarget.value)}
				>
					<option value="">— none —</option>
					<option value="true">true</option>
					<option value="false">false</option>
				</select>
			{:else}
				<input
					id="f-default"
					class="field-input"
					type={isNumeric ? 'number' : 'text'}
					value={field.def.default === undefined ? '' : String(field.def.default)}
					placeholder={field.def.type === 'time'
						? '"now" or a timestamp'
						: field.def.type === 'uuid'
							? 'a uuid'
							: ''}
					onchange={(e) => setDefault(e.currentTarget.value)}
				/>
			{/if}
			{/if}
		</section>

		{#if isFile}
			<!-- File reference (FILES-LINK-S1): target fixed to the engine file store -->
			<section class="p-sec">
				<div class="sec-title">File reference</div>
				<div class="rule-note muted">
					Stores a file_id from the tenant's file store (POST /api/files) with a real
					foreign key — the value must be one of this tenant's files.
				</div>
				<label class="lbl" for="f-od-file">on_delete <span class="muted">(when the FILE is deleted)</span></label>
				<select
					id="f-od-file"
					class="field-select"
					value={field.def.on_delete ?? 'restrict'}
					onchange={(e) => setOnDelete(e.currentTarget.value)}
				>
					<option value="restrict">restrict — the file cannot be deleted while attached</option>
					<option value="set_null">set_null — deleting the file detaches it from the record</option>
				</select>
				<!-- UI-1 (FILES-1): the per-field attach policy — enforced at attach time
				     against the STORED (sniffed) metadata, 422 file_policy on violation. -->
				<label class="lbl" for="f-accept">accept <span class="muted">(what may be attached; empty = anything)</span></label>
				<input
					id="f-accept"
					class="field-input"
					placeholder="image  ·  or: image,application/pdf"
					value={acceptText()}
					onchange={(e) => setAccept(e.currentTarget.value)}
				/>
				<div class="rule-note muted">
					A family (image, audio, video, text), the alias pdf, or an exact type like
					application/zip — comma-separate to allow several.
				</div>
				<label class="lbl" for="f-maxbytes">max_bytes <span class="muted">(max attachable size; empty = no field cap)</span></label>
				<input
					id="f-maxbytes"
					class="field-input"
					type="number"
					min="1"
					placeholder="5242880 (5 MiB)"
					value={field.def.max_bytes ?? ''}
					onchange={(e) => setMaxBytes(e.currentTarget.value)}
				/>
			</section>
		{:else}
		<!-- Relation (foreign key) -->
		<section class="p-sec">
			<div class="sec-title">Foreign key</div>
			<label class="lbl" for="f-rel">References resource</label>
			<select
				id="f-rel"
				class="field-select"
				value={field.def.relation ?? ''}
				onchange={(e) => setRelation(e.currentTarget.value)}
			>
				<option value="">— none —</option>
				{#each editor.entityNames as n}<option value={n}>{n}</option>{/each}
			</select>
			{#if field.def.relation}
				<div class="grid2">
					<div>
						<label class="lbl" for="f-od">on_delete</label>
						<select
							id="f-od"
							class="field-select"
							value={field.def.on_delete ?? 'restrict'}
							onchange={(e) => setOnDelete(e.currentTarget.value)}
						>
							{#each REFERENTIAL_ACTIONS as a}<option value={a}>{a}</option>{/each}
						</select>
					</div>
					<div>
						<label class="lbl" for="f-ou">on_update</label>
						<select
							id="f-ou"
							class="field-select"
							value={field.def.on_update ?? ''}
							onchange={(e) => setOnUpdate(e.currentTarget.value)}
						>
							<option value="">no action</option>
							{#each REFERENTIAL_ACTIONS as a}<option value={a}>{a}</option>{/each}
						</select>
					</div>
				</div>
				<label class="lbl" for="f-ref">references <span class="muted">(target column)</span></label>
				<select
					id="f-ref"
					class="field-select"
					value={field.def.references ?? 'id'}
					onchange={(e) => setReferences(e.currentTarget.value)}
				>
					{#each editor.referenceableColumns(field.def.relation) as c}<option value={c}>{c}</option>{/each}
				</select>
				{#if editor.referenceableColumns(field.def.relation).length === 1}
					<div class="rule-note muted">{field.def.relation} has no unique column besides id.</div>
				{/if}
			{/if}
		</section>
		{/if}

		<!-- Validation rules -->
		<section class="p-sec">
			<div class="sec-title">Validation</div>
			{#if isNumeric}
				<div class="grid2">
					<div>
						<label class="lbl" for="f-min">min</label>
						<input
							id="f-min"
							class="field-input tnum"
							type="number"
							value={field.def.min ?? ''}
							onchange={(e) => setNum('min', e.currentTarget.value)}
						/>
					</div>
					<div>
						<label class="lbl" for="f-max">max</label>
						<input
							id="f-max"
							class="field-input tnum"
							type="number"
							value={field.def.max ?? ''}
							onchange={(e) => setNum('max', e.currentTarget.value)}
						/>
					</div>
				</div>
			{:else if isStringy}
				<div class="grid2">
					<div>
						<label class="lbl" for="f-minl">minLength</label>
						<input
							id="f-minl"
							class="field-input tnum"
							type="number"
							value={field.def.minLength ?? ''}
							onchange={(e) => setNum('minLength', e.currentTarget.value)}
						/>
					</div>
					<div>
						<label class="lbl" for="f-maxl">maxLength</label>
						<input
							id="f-maxl"
							class="field-input tnum"
							type="number"
							value={field.def.maxLength ?? ''}
							onchange={(e) => setNum('maxLength', e.currentTarget.value)}
						/>
					</div>
				</div>
				<label class="lbl" for="f-pat">pattern <span class="muted">(RE2)</span></label>
				<input
					id="f-pat"
					class="field-input"
					value={field.def.pattern ?? ''}
					spellcheck="false"
					onchange={(e) => setStr('pattern', e.currentTarget.value)}
				/>
				<label class="lbl" for="f-fmt">format</label>
				<select
					id="f-fmt"
					class="field-select"
					value={field.def.format ?? ''}
					onchange={(e) => setFormat(e.currentTarget.value)}
				>
					<option value="">— none —</option>
					{#each FIELD_FORMATS as f}<option value={f}>{f}</option>{/each}
				</select>
			{/if}
			{#if !isFile}
				<label class="lbl" for="f-enum">enum <span class="muted">(comma-separated)</span></label>
				<input
					id="f-enum"
					class="field-input"
					value={(field.def.enum ?? []).join(', ')}
					onchange={(e) => setEnum(e.currentTarget.value)}
				/>
			{/if}
			{#if isFile}
				<div class="rule-note muted">
					A file field takes no validation rules — the engine enforces that its value
					references an existing file of the tenant (422 otherwise).
				</div>
			{:else if !isNumeric && !isStringy}
				<div class="rule-note muted">
					{field.def.type} fields take no length/range/pattern rules — only enum and a default.
				</div>
			{/if}
			{#if fieldIssues.length > 0}
				<div class="issues" role="alert">
					{#each fieldIssues as iss}<div class="issue">⚠ {iss}</div>{/each}
				</div>
			{/if}
			{#if isStringy}
				<div class="sm-block">
					{#if field.def.state_machine}
						<button class="btn subtle sm-edit" onclick={() => ui.openStateMachine(entity.id, field.id)}>
							<span class="badge b-sm">SM</span> Edit state machine
							<span class="muted">({smKnownStates(field.def.state_machine).length} states)</span>
						</button>
					{:else}
						<button
							class="btn subtle sm-add"
							onclick={() => {
								editor.enableStateMachine(entity.id, field.id);
								ui.openStateMachine(entity.id, field.id);
							}}
						>
							⮌ Add state machine
						</button>
						<div class="rule-note muted">A lifecycle of states + allowed transitions (G5).</div>
					{/if}
				</div>
			{/if}
		</section>

		<div class="p-foot">
			<button class="btn danger" onclick={() => editor.deleteField(entity.id, field.id)}>
				Delete field
			</button>
		</div>
	{:else if entity}
		<!-- ── ENTITY inspector ────────────────────────────────────────── -->
		<div class="p-head"><span class="crumb-cur">{entity.name}</span></div>

		<section class="p-sec">
			<label class="lbl" for="e-name">Resource name</label>
			<input
				id="e-name"
				class="field-input"
				value={entity.name}
				spellcheck="false"
				onchange={(e) => renameEntity(e.currentTarget.value)}
			/>
			{#if nameError}<div class="err">{nameError}</div>{/if}
			{#if entity.originalName && entity.originalName !== entity.name}
				<div
					class="rename-note"
					title="The deploy runs ALTER TABLE … RENAME: the table and all its rows move to the new name — nothing is dropped or recreated."
				>
					↪ renames “{entity.originalName}” on deploy — existing data is preserved
				</div>
			{/if}
		</section>

		<section class="p-sec">
			<div class="sec-title">
				Fields <span class="count tnum">{entity.fields.length}</span>
				<button class="btn subtle add" onclick={() => editor.addField(entity.id)}>+ add</button>
			</div>
			<div class="field-list">
				{#each entity.fields as f (f.id)}
					<button class="frow" onclick={() => editor.selectField(entity.id, f.id)}>
						<span class="fr-name" class:fk={!!f.def.relation}>{f.name}</span>
						<span class="fr-type">{f.def.type}</span>
					</button>
				{/each}
				{#if entity.fields.length === 0}<div class="muted pad">no fields yet</div>{/if}
			</div>
		</section>

		<EntityDataPanel {entity} />
		<EntityRelationsPanel {entity} />
		<EntityHooksPanel {entity} />

		<section class="p-sec">
			<div class="sec-title">Outbox events</div>
			<div class="flags">
				{#each EVENT_ACTIONS as a}
					<label class="chk"
						><input
							type="checkbox"
							checked={(entity.extras.events ?? []).includes(a)}
							onchange={(e) => toggleEvent(a, e.currentTarget.checked)}
						/> {a}</label
					>
				{/each}
			</div>
		</section>

		<div class="p-foot">
			<button class="btn danger" onclick={() => editor.deleteEntity(entity.id)}>Delete entity</button>
		</div>
	{:else}
		<!-- ── nothing selected ────────────────────────────────────────── -->
		<div class="p-head"><span class="crumb-cur">Schema</span></div>
		<section class="p-sec">
			<label class="lbl" for="s-name">API name</label>
			<input
				id="s-name"
				class="field-input"
				value={editor.schemaName}
				spellcheck="false"
				onchange={(e) => (editor.schemaName = e.currentTarget.value.trim() || 'untitled-api')}
			/>
			<div class="stat-row">
				<div><span class="stat tnum">{editor.entities.length}</span><span class="muted">resources</span></div>
				<div><span class="stat tnum">{editor.edges.length}</span><span class="muted">relations</span></div>
			</div>
		</section>
		<section class="p-sec">
			<p class="tip">
				Select an entity or field on the canvas to edit it here. This panel grows — future
				increments add RBAC, state-machine and index editing in the same place.
			</p>
		</section>
	{/if}
</aside>

<style>
	.panel {
		width: var(--panel-w);
		flex: 0 0 var(--panel-w);
		height: 100%;
		background: var(--surface);
		border-left: 1px solid var(--border);
		display: flex;
		flex-direction: column;
		overflow-y: auto;
	}
	.p-head {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 12px 16px;
		border-bottom: 1px solid var(--border);
		position: sticky;
		top: 0;
		background: var(--surface);
		z-index: 1;
	}
	.crumb {
		border: none;
		background: none;
		color: var(--brand);
		font-weight: 600;
		padding: 0;
	}
	.crumb:hover {
		text-decoration: underline;
	}
	.crumb-sep {
		color: var(--text-3);
	}
	.crumb-cur {
		font-weight: 700;
		font-family: var(--mono);
	}

	.p-sec {
		padding: 14px 16px;
		border-bottom: 1px solid var(--border);
		display: flex;
		flex-direction: column;
		gap: 8px;
	}
	.sec-title {
		display: flex;
		align-items: center;
		gap: 8px;
		font-size: 11px;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-3);
		font-weight: 700;
	}
	.sec-title .add {
		margin-left: auto;
		padding: 2px 8px;
		color: var(--brand);
	}
	.count {
		background: var(--surface-2);
		border-radius: 9px;
		padding: 0 7px;
		color: var(--text-2);
	}
	.lbl {
		font-size: 11px;
		font-weight: 600;
		color: var(--text-2);
		margin-top: 2px;
	}
	.muted {
		color: var(--text-3);
		font-weight: 400;
	}
	.grid2 {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 8px;
	}
	.flags {
		display: flex;
		flex-wrap: wrap;
		gap: 10px;
	}
	.chk {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		font-size: 12.5px;
		color: var(--text);
	}
	.err {
		color: var(--danger);
		font-size: 11.5px;
	}
	.rename-note {
		color: var(--ok, var(--text-2));
		font-size: 11.5px;
		margin-top: 4px;
		cursor: help;
	}

	.field-list {
		display: flex;
		flex-direction: column;
		gap: 1px;
	}
	.frow {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		padding: 5px 8px;
		border: 1px solid transparent;
		border-radius: var(--radius-sm);
		background: var(--surface-2);
		text-align: left;
	}
	.frow:hover {
		border-color: var(--border-strong);
	}
	.fr-name {
		font-family: var(--mono);
		font-size: 12.5px;
	}
	.fr-name.fk {
		color: var(--accent-fk);
	}
	.fr-type {
		font-family: var(--mono);
		font-size: 11px;
		color: var(--text-3);
	}

	.issues {
		display: flex;
		flex-direction: column;
		gap: 4px;
		margin-top: 4px;
		padding: 7px 9px;
		border-radius: var(--radius-sm);
		background: color-mix(in srgb, var(--danger) 8%, transparent);
		border: 1px solid color-mix(in srgb, var(--danger) 35%, transparent);
	}
	.issue {
		font-size: 11.5px;
		line-height: 1.4;
		color: var(--danger);
	}
	.rule-note {
		font-size: 11.5px;
		line-height: 1.4;
		margin-top: 2px;
	}

	.sm-block {
		display: flex;
		flex-direction: column;
		gap: 4px;
		margin-top: 2px;
	}
	.sm-edit,
	.sm-add {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		width: 100%;
		justify-content: flex-start;
		font-size: 12.5px;
	}
	.badge.b-sm {
		background: var(--brand-100);
		color: var(--brand);
		font-size: 10px;
		font-weight: 600;
		padding: 1px 5px;
		border-radius: 5px;
	}

	.p-foot {
		margin-top: auto;
		padding: 14px 16px;
	}
	.stat-row {
		display: flex;
		gap: 22px;
		margin-top: 8px;
	}
	.stat-row > div {
		display: flex;
		flex-direction: column;
	}
	.stat {
		font-size: 22px;
		font-weight: 700;
	}
	.tip {
		font-size: 12.5px;
		color: var(--text-2);
		line-height: 1.5;
		margin: 0;
	}
	.pad {
		padding: 4px 0;
	}
</style>
