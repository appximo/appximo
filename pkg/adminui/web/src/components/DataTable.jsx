import { createSignal, createMemo, For, Show } from "solid-js"
import {
  createSolidTable, flexRender, getCoreRowModel, getSortedRowModel,
} from "@tanstack/solid-table"
import { createVirtualizer } from "@tanstack/solid-virtual"

// DataTable — the reusable table every screen builds on. TanStack Table (headless
// sorting) + TanStack Virtual (windowed rows) from day one, so a list of thousands
// of rows scrolls at 60fps. Sticky header, sortable columns, hover-highlight (NOT
// zebra), first column = a human identifier, actions at the row end.
//
// No skeleton: the backend is local (~ms), so rows render directly; a skeleton
// would flash. An explicit empty state shows when there are zero rows.
export function DataTable(props) {
  const [sorting, setSorting] = createSignal([])

  const table = createMemo(() =>
    createSolidTable({
      get data() { return props.data || [] },
      columns: props.columns,
      state: { get sorting() { return sorting() } },
      onSortingChange: setSorting,
      getCoreRowModel: getCoreRowModel(),
      getSortedRowModel: getSortedRowModel(),
    })
  )

  let scrollEl
  const rows = createMemo(() => table().getRowModel().rows)
  const virtualizer = createMemo(() =>
    createVirtualizer({
      get count() { return rows().length },
      getScrollElement: () => scrollEl,
      estimateSize: () => 45,
      overscan: 12,
    })
  )

  return (
    <div class="tablewrap" ref={scrollEl} style={{ "max-height": props.maxHeight || "calc(100vh - 220px)" }}>
      <table class="dt">
        <thead>
          <For each={table().getHeaderGroups()}>{(hg) => (
            <tr>
              <For each={hg.headers}>{(header) => {
                const canSort = header.column.getCanSort()
                return (
                  <th
                    class={canSort ? "sortable" : ""}
                    style={header.column.columnDef.meta?.align === "right" ? { "text-align": "right" } : undefined}
                    onClick={canSort ? header.column.getToggleSortingHandler() : undefined}
                    aria-sort={header.column.getIsSorted() === "asc" ? "ascending" : header.column.getIsSorted() === "desc" ? "descending" : "none"}
                  >
                    {flexRender(header.column.columnDef.header, header.getContext())}
                    <Show when={header.column.getIsSorted()}>
                      <span aria-hidden="true">{header.column.getIsSorted() === "asc" ? " ↑" : " ↓"}</span>
                    </Show>
                  </th>
                )
              }}</For>
            </tr>
          )}</For>
        </thead>
        <tbody style={{ height: `${virtualizer().getTotalSize()}px`, position: "relative", display: "block" }}>
          <Show when={rows().length > 0} fallback={
            <tr><td colspan={props.columns.length}><div class="empty">{props.emptyMessage || "Nothing here yet."}</div></td></tr>
          }>
            <For each={virtualizer().getVirtualItems()}>{(vrow) => {
              const row = rows()[vrow.index]
              return (
                <tr
                  style={{
                    position: "absolute", top: 0, left: 0, width: "100%",
                    display: "table", "table-layout": "fixed",
                    transform: `translateY(${vrow.start}px)`,
                  }}
                >
                  <For each={row.getVisibleCells()}>{(cell) => (
                    <td style={cell.column.columnDef.meta?.align === "right" ? { "text-align": "right" } : undefined}>
                      {flexRender(cell.column.columnDef.cell, cell.getContext())}
                    </td>
                  )}</For>
                </tr>
              )
            }}</For>
          </Show>
        </tbody>
      </table>
    </div>
  )
}
