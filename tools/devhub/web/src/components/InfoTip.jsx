import { createSignal, Show, onCleanup } from 'solid-js'

// InfoTip (S47c): discreet ⓘ next to a title/label that opens a small dark
// popover explaining what the number actually measures. Plain Solid +
// Tailwind — no tooltip libraries. Click or hover opens; click outside or
// Escape closes.
export default function InfoTip(props) {
  const [open, setOpen] = createSignal(false)
  let rootRef

  const onDocClick = (e) => { if (rootRef && !rootRef.contains(e.target)) setOpen(false) }
  const onKey = (e) => { if (e.key === 'Escape') setOpen(false) }

  const show = () => {
    if (open()) return
    setOpen(true)
    document.addEventListener('click', onDocClick)
    document.addEventListener('keydown', onKey)
  }
  const hide = () => {
    setOpen(false)
    document.removeEventListener('click', onDocClick)
    document.removeEventListener('keydown', onKey)
  }
  onCleanup(hide)

  return (
    <span ref={rootRef} class="relative inline-block align-middle">
      <button
        type="button"
        aria-label={props.label ?? 'Qué mide esto'}
        aria-expanded={open()}
        onClick={(e) => { e.stopPropagation(); open() ? hide() : show() }}
        onMouseEnter={show}
        class="text-slate-500 hover:text-slate-300 text-xs leading-none px-0.5 cursor-help select-none">
        ⓘ
      </button>
      <Show when={open()}>
        <div
          role="tooltip"
          onMouseLeave={hide}
          class="absolute left-0 top-full mt-1 z-50 w-max max-w-xs rounded border border-slate-600
                 bg-slate-800 p-2.5 text-xs leading-relaxed text-slate-300 shadow-lg shadow-black/40 normal-case tracking-normal text-left">
          {props.children}
        </div>
      </Show>
    </span>
  )
}
