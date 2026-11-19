import { useState } from "react"
import { TREE_MATRIX_LEGEND, type TreeMatrixData } from "@/lib/tree-matrix"

const CELL_CLASS: Record<number, string> = {
  0: "bg-muted/20",
  1: "bg-sky-500/35",
  2: "bg-amber-400/60",
  3: "bg-emerald-500/65",
  4: "bg-slate-500/55",
}

function short(label: string, max = 16): string {
  return label.length > max ? `${label.slice(0, max - 1)}…` : label
}

/** Матрица связей дерева: строки и столбцы - узлы, цвет - тип связи. */
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
    <div className="space-y-3">
      <div className="flex flex-wrap gap-3 text-xs text-muted-foreground">
        {TREE_MATRIX_LEGEND.map((item) => (
          <span key={item.value} className="inline-flex items-center gap-1.5">
            <span className={`size-2.5 rounded-sm ${item.color}`} />
            {item.label}
          </span>
        ))}
        <span className="ml-auto">Узлов: {data.nodes.length}</span>
      </div>
      <div className="overflow-x-auto rounded-xl border bg-muted/10 p-2">
        <table className="border-separate border-spacing-1 text-[10px]">
          <thead>
            <tr>
              <th className="sticky left-0 z-10 w-44 bg-card p-1 text-left font-medium text-muted-foreground">
                Узел
              </th>
              {data.nodes.map((node, column) => (
                <th key={node.id} className="h-24 w-9 p-0 align-bottom">
                  <div
                    className={`mx-auto origin-bottom-left -rotate-45 whitespace-nowrap px-1 pb-1 ${
                      hovered === column ? "text-foreground" : "text-muted-foreground"
                    }`}
                    title={node.title}
                  >
                    {short(node.title, 17)}
                  </div>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {data.nodes.map((rowNode, row) => (
              <tr key={rowNode.id} onMouseEnter={() => setHovered(row)} onMouseLeave={() => setHovered(null)}>
                <th
                  className={`sticky left-0 z-10 max-w-44 truncate bg-card px-1 text-left font-normal ${
                    hovered === row ? "text-foreground" : "text-muted-foreground"
                  }`}
                  title={rowNode.title}
                >
                  {short(rowNode.title)}
                </th>
                {data.matrix[row].map((value, column) => (
                  <td
                    key={`${row}-${column}`}
                    className={`h-7 w-9 rounded-sm text-center align-middle font-medium text-foreground/80 transition-opacity ${
                      CELL_CLASS[value] ?? CELL_CLASS[0]
                    } ${hovered === null || hovered === row || hovered === column ? "opacity-100" : "opacity-35"}`}
                    title={`${rowNode.title} × ${data.nodes[column].title}: ${
                      TREE_MATRIX_LEGEND.find((item) => item.value === value)?.label ?? "нет связи"
                    }`}
                  >
                    {value === 4 ? "•" : value === 0 ? "" : value}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {data.truncated > 0 && (
        <p className="text-xs text-muted-foreground">
          В матрице первые {data.nodes.length} узлов; ещё {data.truncated} скрыто.
        </p>
      )}
      <p className="text-xs text-muted-foreground">
        Строки и столбцы - узлы дерева. 3 - прямая связь «родитель → ребёнок», 2 - соседи в одной
        ветке, 1 - общий источник цитат. Наведите на строку, чтобы выделить её.
      </p>
    </div>
  )
}
