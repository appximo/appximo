<script lang="ts">
	import { editor } from '../stores/editor.svelte';
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

	const entity = $derived(editor.selectedEntity);
	const field = $derived(editor.selectedField);

	let nameError = $state<string | null>(null);
	let fieldNameError = $state<string | null>(null);

	const NUMERIC = new Set<FieldType>(['int', 'int64', 'float64']);
	const STRINGY = new Set<FieldType>(['string', 'text']);
	const isNumeric = $derived(field ? NUMERIC.has(field.def.type) : false);
	const isStringy = $derived(field ? STRINGY.has(field.def.type) : false);

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
		if (entity && field) editor.patchFieldDef(entity.id, field.id, 'type', t);
	}
	function toggleFlag(key: 'required' | 'unique' | 'auto', on: boolean) {
		if (entity && field) editor.patchFieldDef(entity.id, field.id, key, on ? true : undefined);
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
		if (isNumeric) {
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
			return;
		}
		editor.patchFieldDef(entity.id, field.id, 'type', 'uuid');
		editor.patchFieldDef(entity.id, field.id, 'relation', target);
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
				<label class="chk"
					><input
						type="checkbox"
						checked={!!field.def.auto}
						onchange={(e) => toggleFlag('auto', e.currentTarget.checked)}
					/> auto</label
				>
			</div>

			<label class="lbl" for="f-default">Default <span class="muted">(on create)</span></label>
			<input
				id="f-default"
				class="field-input"
				value={field.def.default === undefined ? '' : String(field.def.default)}
				placeholder={field.def.type === 'time' ? '"now" or a timestamp' : ''}
				onchange={(e) => setDefault(e.currentTarget.value)}
			/>
		</section>

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
			{/if}
		</section>

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
			<label class="lbl" for="f-enum">enum <span class="muted">(comma-separated)</span></label>
			<input
				id="f-enum"
				class="field-input"
				value={(field.def.enum ?? []).join(', ')}
				onchange={(e) => setEnum(e.currentTarget.value)}
			/>
			{#if field.def.state_machine}
				<div class="sm-note">
					<span class="badge b-sm">SM</span> state machine declared
					<span class="muted">(visual editing coming soon)</span>
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
			{#if entity.relations.length > 0}
				<div class="sec-title" style="margin-top:14px">Declared embeds</div>
				<div class="rel-list">
					{#each entity.relations as r (r.id)}
						<div class="rel-row">
							<span class="rr-name">{r.name}</span>
							<span class="rr-meta">{r.def.type} → {r.def.target}</span>
						</div>
					{/each}
				</div>
			{/if}
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

	.rel-list {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.rel-row {
		display: flex;
		justify-content: space-between;
		gap: 8px;
		font-size: 12px;
	}
	.rr-name {
		font-family: var(--mono);
	}
	.rr-meta {
		color: var(--text-3);
		font-size: 11px;
	}

	.sm-note {
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: 12px;
		color: var(--text-2);
	}
	.badge.b-sm {
		background: color-mix(in srgb, var(--ok) 16%, transparent);
		color: var(--ok);
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
