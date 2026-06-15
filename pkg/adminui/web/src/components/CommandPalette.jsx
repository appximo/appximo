import { createSignal, createMemo, For, Show, onMount, onCleanup } from "solid-js"

// CommandPalette — Cmd/Ctrl+K. Implemented natively (≈70 lines) instead of pulling
// cmdk-solid: it keeps the bundle lean and dependency surface minimal (the project
// ethos), while delivering the same UX — fuzzy-ish filtering, full keyboard nav,
// accessible listbox semantics. `commands` is a flat list of { id, label, hint, run }.
export function CommandPalette(props) {
  const [open, setOpen] = createSignal(false)
  const [query, setQuery] = createSignal("")
  const [active, setActive] = createSignal(0)
  let inputEl

  const onKey = (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
      e.preventDefault()
      setOpen((o) => !o)
    } else if (e.key === "Escape" && open()) {
      setOpen(false)
    }
  }
  onMount(() => window.addEventListener("keydown", onKey))
  onCleanup(() => window.removeEventListener("keydown", onKey))

  const filtered = createMemo(() => {
    const q = query().trim().toLowerCase()
    const list = props.commands() || []
    if (!q) return list
    return list.filter((c) => c.label.toLowerCase().includes(q) || (c.hint || "").toLowerCase().includes(q))
  })

  const run = (cmd) => {
    setOpen(false)
    setQuery("")
    cmd?.run?.()
  }

  const onInputKey = (e) => {
    const items = filtered()
    if (e.key === "ArrowDown") { e.preventDefault(); setActive((a) => Math.min(a + 1, items.length - 1)) }
    else if (e.key === "ArrowUp") { e.preventDefault(); setActive((a) => Math.max(a - 1, 0)) }
    else if (e.key === "Enter") { e.preventDefault(); run(items[active()]) }
  }

  return (
    <Show when={open()}>
      <div class="cmdk-backdrop" onClick={(e) => { if (e.target === e.currentTarget) setOpen(false) }}>
        <div class="cmdk" role="dialog" aria-label="Command palette">
          <input
            ref={(el) => { inputEl = el; queueMicrotask(() => el.focus()) }}
            placeholder="Type a command or search…"
            value={query()}
            onInput={(e) => { setQuery(e.currentTarget.value); setActive(0) }}
            onKeyDown={onInputKey}
            aria-activedescendant={`cmd-${active()}`}
            role="combobox" aria-expanded="true" aria-controls="cmdk-list"
          />
          <ul id="cmdk-list" role="listbox">
            <For each={filtered()}>{(cmd, i) => (
              <li id={`cmd-${i()}`} role="option" aria-selected={i() === active()}
                onMouseEnter={() => setActive(i())} onClick={() => run(cmd)}>
                <span>{cmd.label}</span>
                <Show when={cmd.hint}><span class="spacer" /><span class="muted">{cmd.hint}</span></Show>
              </li>
            )}</For>
            <Show when={filtered().length === 0}>
              <li class="muted" aria-disabled="true">No matches</li>
            </Show>
          </ul>
        </div>
      </div>
    </Show>
  )
}
