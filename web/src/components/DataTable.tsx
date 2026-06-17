import type { ReactNode } from 'react'

export interface Column<T> {
  /** Header label. */
  header: string
  /** Cell renderer for a row. */
  cell: (row: T) => ReactNode
  /** Optional extra classes for the cell (e.g. `font-mono` for ids/numerics). */
  className?: string
}

interface DataTableProps<T> {
  columns: Column<T>[]
  rows: T[]
  rowKey: (row: T) => string
  /** Shown when there are no rows. */
  empty?: ReactNode
}

/** A compact, dense table with hover rows and --color-border dividers. Numeric/id columns get
 *  `font-mono` via the column className. */
export function DataTable<T>({ columns, rows, rowKey, empty }: DataTableProps<T>) {
  if (rows.length === 0) {
    return <div className="px-3 py-6 text-center text-xs text-muted-foreground">{empty ?? 'No data'}</div>
  }
  return (
    <table className="w-full border-collapse text-xs">
      <thead>
        <tr className="border-b border-border text-left text-muted-foreground">
          {columns.map((c) => (
            <th key={c.header} className="px-3 py-1.5 font-medium">
              {c.header}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((row) => (
          <tr key={rowKey(row)} className="border-b border-border/60 hover:bg-muted/40">
            {columns.map((c) => (
              <td key={c.header} className={`px-3 py-1.5 align-middle ${c.className ?? ''}`}>
                {c.cell(row)}
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  )
}
