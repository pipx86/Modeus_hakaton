import type { SkillGraphResponse } from "@/lib/api/client"

const VIEW_WIDTH = 640
const VIEW_HEIGHT = 340
const CENTER_X = VIEW_WIDTH / 2
const CENTER_Y = VIEW_HEIGHT / 2

/** Круговая раскладка графа компетенций: размер узла и толщина связи отражают частоту. */
export function SkillGraph({ data }: { data: SkillGraphResponse }) {
  if (data.nodes.length === 0) {
    return (
      <p className="rounded-lg border border-dashed p-4 text-sm text-muted-foreground">
        Нужны завершённые исследования с функциями, задачами или действиями в дереве.
      </p>
    )
  }
  const radius = Math.min(VIEW_WIDTH, VIEW_HEIGHT) * 0.32
  const positions = new Map(
    data.nodes.map((node, index) => {
      const angle = (Math.PI * 2 * index) / data.nodes.length - Math.PI / 2
      return [
        node.id,
        {
          x: CENTER_X + Math.cos(angle) * radius,
          y: CENTER_Y + Math.sin(angle) * radius,
        },
      ]
    }),
  )
  const maxCount = Math.max(...data.nodes.map((node) => node.count), 1)
  return (
    <div className="space-y-2">
      <svg
        viewBox={`0 0 ${VIEW_WIDTH} ${VIEW_HEIGHT}`}
        className="h-auto w-full"
        role="img"
        aria-label="Граф связей компетенций по завершённым исследованиям"
      >
        {data.edges.map((edge) => {
          const source = positions.get(edge.source)
          const target = positions.get(edge.target)
          if (!source || !target) return null
          return (
            <line
              key={`${edge.source}-${edge.target}`}
              x1={source.x}
              y1={source.y}
              x2={target.x}
              y2={target.y}
              stroke="currentColor"
              className="text-primary/25"
              strokeWidth={1 + Math.min(4, edge.weight * 0.6)}
            >
              <title>{`Связь: ${edge.weight}`}</title>
            </line>
          )
        })}
        {data.nodes.map((node) => {
          const position = positions.get(node.id)!
          const nodeRadius = 7 + (node.count / maxCount) * 9
          return (
            <g key={node.id}>
              <circle
                cx={position.x}
                cy={position.y}
                r={nodeRadius}
                className="fill-primary/70 stroke-primary"
                strokeWidth={1.5}
              >
                <title>{`${node.label}: ${node.count} исследований`}</title>
              </circle>
              <text
                x={position.x}
                y={position.y + nodeRadius + 12}
                textAnchor="middle"
                className="fill-foreground text-[10px]"
              >
                {node.label.length > 22 ? `${node.label.slice(0, 21)}…` : node.label}
              </text>
            </g>
          )
        })}
      </svg>
      <p className="text-xs text-muted-foreground">
        Узлы: функции, задачи и действия; размер - частота, линии - совместная встречаемость.
        Исследований: {data.sample_size}.
      </p>
    </div>
  )
}
