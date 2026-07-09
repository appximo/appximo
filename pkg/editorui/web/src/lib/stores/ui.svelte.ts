// UI/presentation state (theme). Separate from the editor model store so the two
// concerns grow independently. Persisted to localStorage — this is a served app,
// not an artifact, so localStorage is appropriate (same rationale as the admin UI).

type Theme = 'light' | 'dark';

class UIStore {
	theme = $state<Theme>('light');
	/** The main workspace view: the ERD canvas or the JSON code editor (JSON-EDITOR-S2). */
	view = $state<'canvas' | 'code'>('canvas');
	/** The Code view's buffer, preserved across view switches ONLY while it has
	 *  unapplied edits (null = re-snapshot from the model on next open). */
	codeBuffer = $state<string | null>(null);
	/** Whether the Roles & Permissions (RBAC) editor is open. */
	rbacOpen = $state(false);
	/** The field whose state-machine designer is open ({entityId, fieldId}), or null. */
	smTarget = $state<{ entityId: string; fieldId: string } | null>(null);

	openStateMachine(entityId: string, fieldId: string) {
		this.smTarget = { entityId, fieldId };
	}
	closeStateMachine() {
		this.smTarget = null;
	}

	init() {
		if (typeof localStorage !== 'undefined') {
			const t = localStorage.getItem('appitools-editor-theme');
			if (t === 'dark' || t === 'light') this.theme = t;
		}
		this.apply();
	}

	toggle() {
		this.theme = this.theme === 'light' ? 'dark' : 'light';
		try {
			localStorage.setItem('appitools-editor-theme', this.theme);
		} catch {
			/* private mode — ignore */
		}
		this.apply();
	}

	private apply() {
		if (typeof document !== 'undefined') {
			document.documentElement.dataset.theme = this.theme;
		}
	}
}

export const ui = new UIStore();
