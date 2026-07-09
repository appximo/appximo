// Vite-aliased replacement for codemirror-json-schema's utils/markdown module
// (see vite.config.ts). The real module renders hover/lint markdown through
// markdown-it + shiki — a multi-megabyte dependency chain for what is, in our
// meta-schema, plain sentences with occasional `code` spans. This stub renders
// exactly that (escape + backtick→<code>, newline→<br>), keeping the bundle in
// the "potente y liviano" budget. The output is used via innerHTML upstream, so
// everything is escaped first.

function escapeHTML(s: string): string {
	return s
		.replaceAll('&', '&amp;')
		.replaceAll('<', '&lt;')
		.replaceAll('>', '&gt;')
		.replaceAll('"', '&quot;');
}

export function renderMarkdown(markdown: string, inline = true): string {
	let html = escapeHTML(markdown).replace(/`([^`]+)`/g, '<code>$1</code>');
	if (!inline) html = html.replaceAll('\n', '<br>');
	return html;
}
