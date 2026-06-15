import { createSignal, createMemo, For, Show } from "solid-js"
import {
  createSolidTable, flexRender, getCoreRowModel, getSortedRowModel,
} from "@tanstack/solid-table"

// Virtualización removida — re-agregar con TanStack Virtual cuando un recurso tenga >1000 filas
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
      defaultColumn: { size: 150, minSize: 50, maxSize: 800 },
    })
  )

  const totalSize = createMemo(() => table().getTotalSize())
  const colPct = (col) => `${(col.getSize() / totalSize()) * 100}%`

  return (
    <div class="tablewrap" style={{ "max-height": props.maxHeight || "calc(100vh - 220px)", "overflow-y": "auto" }}>
      <table class="dt" style={{ "table-layout": "fixed", width: "100%" }}>
        <thead>
          <For each={table().getHeaderGroups()}>{(hg) => (
            <tr>
              <For each={hg.headers}>{(header) => {
                const canSort = header.column.getCanSort()
                return (
                  <th
                    class={canSort ? "sortable" : ""}
                    style={{
                      width: colPct(header.column),
                      "text-align": header.column.columnDef.meta?.align === "right" ? "right" : "left",
                    }}
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
        <tbody>
          <Show when={table().getRowModel().rows.length > 0} fallback={
            <tr><td colspan={props.columns.length}><div class="empty">{props.emptyMessage || "Nothing here yet."}</div></td></tr>
          }>
            <For each={table().getRowModel().rows}>{(row) => (
              <tr>
                <For each={row.getVisibleCells()}>{(cell) => (
                  <td style={{
                    width: colPct(cell.column),
                    "text-align": cell.column.columnDef.meta?.align === "right" ? "right" : undefined,
                  }}>
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </td>
                )}</For>
              </tr>
            )}</For>
          </Show>
        </tbody>
      </table>
    </div>
  )
}
