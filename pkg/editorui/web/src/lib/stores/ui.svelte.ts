// UI/presentation state (theme). Separate from the editor model store so the two
// concerns grow independently. Persisted to localStorage — this is a served app,
// not an artifact, so localStorage is appropriate (same rationale as the admin UI).

type Theme = 'light' | 'dark';

class UIStore {
	theme = $state<Theme>('light');

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
