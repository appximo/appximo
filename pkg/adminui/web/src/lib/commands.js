import { createSignal } from "solid-js"

// A tiny global command registry so a page (e.g. Tenants) can contribute palette
// actions ("Create tenant") on mount and remove them on cleanup, while the Shell
// owns the base navigation/theme/logout commands.
const [pageCommands, setPageCommands] = createSignal([])

export { pageCommands }

export function registerCommands(cmds) {
  setPageCommands(cmds)
  return () => setPageCommands([]) // cleanup
}
