import { useState } from "react"
import { TREE_MATRIX_LEGEND, type TreeMatrixData } from "@/lib/tree-matrix"

const CELL_COLOR: Record<number, string> = {
  0: "bg-slate-700/45",
  1: "bg-sky-700/70",
  2: "bg-amber-500/80",
  3: "bg-orange-400/90",
  4: "bg-sky-400",
}

function short(label: string, max: number): string {
  return label.length > max ? `${label.slice(0, max - 1)}…` : label
}

/** Квадратная матрица совместной встречаемости узлов дерева. */
export function TreeMatrix({ data }: { data: TreeMatrixData }) {
  const [hovered, setHovered] = useState<number | null>(null)
  if (data.nodes.length === 0) {
    return (
      <p className="rounded-lg border border-dashed p-4 text-sm text-muted-foreground">
        В дереве пока нет функций, задач или действий.
      </p>
    )
  }
  return (
    <div className="rounded-xl border bg-card p-4">
      <h3 className="text-lg font-semibold tracking-tight">Матрица совместной встречаемости</h3>
      <p className="mt-1 text-sm text-muted-foreground">
        Аналитично и масштабируется; видно силу каждой пары.
      </p>

      <div className="mt-4 overflow-x-auto">
        <table className="border-separate border-spacing-1 text-[9px]">
          <thead>
            <tr>
              <th className="w-32 p-0" />
              {data.nodes.map((node, column) => (
                <th
                  key={node.id}
                  className={`h-8 w-8 p-0 text-center align-bottom font-medium ${
                    hovered === column ? "text-foreground" : "text-muted-foreground"
                  }`}
                  title={node.title}
                >
                  {short(node.title, 7)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {data.nodes.map((rowNode, row) => (
              <tr
                key={rowNode.id}
                onMouseEnter={() => setHovered(row)}
                onMouseLeave={() => setHovered(null)}
              >
                <th
                  className={`max-w-32 truncate pr-1 text-left font-normal ${
                    hovered === row ? "text-foreground" : "text-muted-foreground"
                  }`}
                  title={rowNode.title}
                >
                  {short(rowNode.title, 18)}
                </th>
                {data.matrix[row].map((value, column) => (
                  <td
                    key={`${row}-${column}`}
                    className={`size-8 rounded-[4px] transition-opacity ${
                      CELL_COLOR[value] ?? CELL_COLOR[0]
                    } ${hovered === null || hovered === row || hovered === column ? "opacity-100" : "opacity-35"}`}
                    title={`${rowNode.title} × ${data.nodes[column].title}: ${
                      TREE_MATRIX_LEGEND.find((item) => item.value === value)?.label ?? "нет связи"
                    }`}
                  />
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="mt-4 flex flex-wrap gap-3 text-[11px] text-muted-foreground">
        {TREE_MATRIX_LEGEND.map((item) => (
          <span key={item.value} className="inline-flex items-center gap-1.5">
            <span className="size-2.5 rounded-[2px]" style={{ backgroundColor: item.color }} />
            {item.label}
          </span>
        ))}
        {data.truncated > 0 && <span>· скрыто узлов: {data.truncated}</span>}
      </div>
    </div>
  )
}
