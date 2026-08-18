// i18n.js — the back-office UI strings, Spanish + English. The LANGUAGE of the
// CHROME only: resource names, field keys, enum values and engine error
// messages are the consumer's own vocabulary and are shown verbatim.
// Selection: a persisted explicit choice wins; otherwise the browser language
// (es* → Spanish, anything else → English), so an existing English-browser
// user sees exactly what they saw before this file existed.

const DICT = {
  es: {
    'login.generated': 'panel generado desde /openapi.json',
    'login.email': 'correo',
    'login.password': 'contraseña',
    'login.signin': 'Entrar',
    'login.signup': 'Crear cuenta',
    'login.help': 'Entre como usuario del inquilino (las credenciales que imprimió <code>appximo up</code>, o un usuario creado en /admin). La sesión vive en memoria: recargar la página la cierra.',
    'login.tenantHint': 'Esta página habla con la API de <b>este mismo origen</b>, y el motor lee el inquilino del subdominio. Ábrala como <code>http://&lt;inquilino&gt;.{host}/app</code> (la URL que imprimió <code>appximo up</code>) — desde un host sin subdominio el inicio de sesión va a fallar.',
    'nav.resources': 'Recursos',
    'nav.engine': 'Motor',
    'nav.logout': 'Salir',
    'nav.denied': 'su rol no tiene acceso',
    'list.search': 'Buscar…',
    'list.new': 'Nuevo',
    'list.empty': 'Sin registros todavía.',
    'list.emptySearch': 'Nada coincide con esa búsqueda.',
    'list.first': 'Primera página',
    'list.next': 'Siguiente',
    'list.retry': 'Reintentar',
    'list.loading': 'Cargando…',
    'list.confirmDelete': '¿Eliminar este registro de {res}?',
    'list.actions': 'Acciones',
    'form.edit': 'Editar',
    'form.new': 'Nuevo registro',
    'form.save': 'Guardar',
    'form.create': 'Crear',
    'form.cancel': 'Cancelar',
    'form.terminal': 'estado terminal — sin movimientos legales',
    'form.none': '—',
    'file.uploaded': 'subido ✓',
    'file.accepts': 'acepta: {list}',
    'file.max': 'máx. {mb} MiB',
    'file.attached': 'adjunto: {id}…',
    'boot.failed': 'No se pudo iniciar: {msg}',
    'theme.auto': 'Tema: automático',
    'theme.light': 'Tema: claro',
    'theme.dark': 'Tema: oscuro',
    'demo.banner': 'Está probando: los cambios no se guardan. Al recargar, la demo vuelve a su estado original.',
    'demo.tag': 'demo',
  },
  en: {
    'login.generated': 'back-office generated from /openapi.json',
    'login.email': 'email',
    'login.password': 'password',
    'login.signin': 'Log in',
    'login.signup': 'Sign up',
    'login.help': 'Sign in as a tenant user (the credentials <code>appximo up</code> printed, or a user created in /admin). The session lives in memory — a refresh signs you out.',
    'login.tenantHint': 'This page talks to the API on <b>this origin</b>, and the engine reads the tenant from the subdomain. Open it as <code>http://&lt;tenant&gt;.{host}/app</code> (the URL <code>appximo up</code> printed) — signing in from a bare host will fail.',
    'nav.resources': 'Resources',
    'nav.engine': 'Engine',
    'nav.logout': 'Log out',
    'nav.denied': 'your role has no access',
    'list.search': 'Search…',
    'list.new': 'New',
    'list.empty': 'No records yet.',
    'list.emptySearch': 'Nothing matches that search.',
    'list.first': 'First page',
    'list.next': 'Next',
    'list.retry': 'Retry',
    'list.loading': 'Loading…',
    'list.confirmDelete': 'Delete this {res} record?',
    'list.actions': 'Actions',
    'form.edit': 'Edit',
    'form.new': 'New record',
    'form.save': 'Save',
    'form.create': 'Create',
    'form.cancel': 'Cancel',
    'form.terminal': 'terminal state — no legal moves',
    'form.none': '—',
    'file.uploaded': 'uploaded ✓',
    'file.accepts': 'accepts: {list}',
    'file.max': 'max {mb} MiB',
    'file.attached': 'attached: {id}…',
    'boot.failed': 'Failed to start: {msg}',
    'theme.auto': 'Theme: auto',
    'theme.light': 'Theme: light',
    'theme.dark': 'Theme: dark',
    'demo.banner': "You're trying things out: changes are not saved. Reloading returns the demo to its original state.",
    'demo.tag': 'demo',
  },
};

const LANG_KEY = 'appximo.app.lang';

export function detectLang() {
  const saved = localStorage.getItem(LANG_KEY);
  if (saved && DICT[saved]) return saved;
  return (navigator.language || '').toLowerCase().startsWith('es') ? 'es' : 'en';
}

export let lang = detectLang();

export function setLang(l) {
  if (!DICT[l]) return;
  lang = l;
  localStorage.setItem(LANG_KEY, l);
  document.documentElement.lang = l;
}

export function t(key, vars = {}) {
  let s = DICT[lang]?.[key] ?? DICT.en[key] ?? key;
  for (const [k, v] of Object.entries(vars)) s = s.replace('{' + k + '}', v);
  return s;
}
