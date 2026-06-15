import { Dialog } from "@ark-ui/solid/dialog"
import { Portal } from "solid-js/web"
import { Show } from "solid-js"

// Modal — a controlled dialog over Ark UI's accessible Dialog primitive (focus
// trap, escape-to-close, aria wiring). Chosen from the prompt's allowed set
// (Ark UI) because dialogs benefit most from a11y primitives.
export function Modal(props) {
  return (
    <Dialog.Root open={props.open} onOpenChange={(d) => props.onClose?.(d.open)} closeOnInteractOutside={!props.busy}>
      <Portal>
        <Dialog.Backdrop class="backdrop" />
        <Dialog.Positioner>
          <Dialog.Content class="dialog">
            <Dialog.Title>{props.title}</Dialog.Title>
            <Show when={props.description}>
              <p class="desc">{props.description}</p>
            </Show>
            {props.children}
          </Dialog.Content>
        </Dialog.Positioner>
      </Portal>
    </Dialog.Root>
  )
}
