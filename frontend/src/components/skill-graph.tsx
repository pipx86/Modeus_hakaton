import { useId, useMemo, useState } from "react"
import type { SkillGraphResponse } from "@/lib/api/client"

const VIEW_WIDTH = 720
const VIEW_HEIGHT = 390
const CENTER_X = VIEW_WIDTH / 2
const CENTER_Y = VIEW_HEIGHT / 2
const TYPE_STYLE: Record<string, { color: string; label: string }> = {
  function: { color: "#38bdf8", label: "Функции" },
  task: { color: "#f59e0b", label: "Задачи" },
  action: { color: "#34d399", label: "Действия" },
}

interface Point {
  x: number
  y: number
}

/** Детерминированная force-layout раскладка без внешних зависимостей. */
function layout(
  nodes: SkillGraphResponse["nodes"],
  edges: SkillGraphResponse["edges"],
): Map<string, Point> {
  const positions = new Map<string, Point>()
  nodes.forEach((node, index) => {
    const angle = (Math.PI * 2 * index) / Math.max(nodes.length, 1) - Math.PI / 2
    positions.set(node.id, {
      x: CENTER_X + Math.cos(angle) * Math.min(150, nodes.length * 18),
      y: CENTER_Y + Math.sin(angle) * Math.min(120, nodes.length * 15),
    })
  })
  const area = VIEW_WIDTH * VIEW_HEIGHT
  const ideal = Math.sqrt(area / Math.max(nodes.length, 1)) * 0.62
  for (let iteration = 0; iteration < 90; iteration += 1) {
    const forces = new Map(nodes.map((node) => [node.id, { x: 0, y: 0 }]))
    for (let left = 0; left < nodes.length; left += 1) {
      for (let right = left + 1; right < nodes.length; right += 1) {
        const a = positions.get(nodes[left].id)!
        const b = positions.get(nodes[right].id)!
        const dx = b.x - a.x
        const dy = b.y - a.y
        const distance = Math.max(1, Math.hypot(dx, dy))
        const force = (ideal * ideal) / distance
        const ux = (dx / distance) * force
        const uy = (dy / distance) * force
        forces.get(nodes[left].id)!.x -= ux
        forces.get(nodes[left].id)!.y -= uy
        forces.get(nodes[right].id)!.x += ux
        forces.get(nodes[right].id)!.y += uy
      }
    }
    for (const edge of edges) {
      const a = positions.get(edge.source)
      const b = positions.get(edge.target)
      if (!a || !b) continue
      const dx = b.x - a.x
      const dy = b.y - a.y
      const distance = Math.max(1, Math.hypot(dx, dy))
      const force = (distance * distance) / ideal
      const ux = (dx / distance) * force * 0.02
      const uy = (dy / distance) * force * 0.02
      forces.get(edge.source)!.x += ux
      forces.get(edge.source)!.y += uy
      forces.get(edge.target)!.x -= ux
      forces.get(edge.target)!.y -= uy
    }
    for (const node of nodes) {
      const position = positions.get(node.id)!
      const force = forces.get(node.id)!
      position.x = Math.min(
        VIEW_WIDTH - 80,
        Math.max(80, position.x + force.x + (CENTER_X - position.x) * 0.012),
      )
      position.y = Math.min(
        VIEW_HEIGHT - 58,
        Math.max(52, position.y + force.y + (CENTER_Y - position.y) * 0.012),
      )
    }
  }
  return positions
}

function labelOf(value: string): string {
  return value.length > 20 ? `${value.slice(0, 19)}…` : value
}

/** Граф компетенций: сила связи - совместная встречаемость в деревьях. */
export function SkillGraph({ data }: { data: SkillGraphResponse }) {
  const glowID = useId().replace(/:/g, "")
  const [hovered, setHovered] = useState<string | null>(null)
  const positions = useMemo(() => layout(data.nodes, data.edges), [data.nodes, data.edges])
  if (data.nodes.length === 0) {
    return (
      <p className="rounded-lg border border-dashed p-4 text-sm text-muted-foreground">
        Нужны завершённые исследования с функциями, задачами или действиями в дереве.
      </p>
    )
  }
  const maxCount = Math.max(...data.nodes.map((node) => node.count), 1)
  const maxWeight = Math.max(...data.edges.map((edge) => edge.weight), 1)
  const neighbourIDs = new Set<string>()
  if (hovered) {
    neighbourIDs.add(hovered)
    for (const edge of data.edges) {
      if (edge.source === hovered) neighbourIDs.add(edge.target)
      if (edge.target === hovered) neighbourIDs.add(edge.source)
    }
  }
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap gap-3 text-xs text-muted-foreground">
        {Object.entries(TYPE_STYLE).map(([type, style]) => {
          const count = data.nodes.filter((node) => node.type === type).length
          if (count === 0) return null
          return (
            <span key={type} className="inline-flex items-center gap-1.5">
              <span className="size-2 rounded-full" style={{ backgroundColor: style.color }} />
              {style.label}: {count}
            </span>
          )
        })}
        <span className="ml-auto">Исследований: {data.sample_size}</span>
      </div>
      <svg
        viewBox={`0 0 ${VIEW_WIDTH} ${VIEW_HEIGHT}`}
        className="h-auto w-full rounded-xl border border-border/60 bg-gradient-to-b from-muted/25 to-transparent"
        role="img"
        aria-label="Граф связей компетенций по завершённым исследованиям"
      >
        <defs>
          <filter id={glowID} x="-50%" y="-50%" width="200%" height="200%">
            <feGaussianBlur stdDeviation="4" result="blur" />
            <feMerge>
              <feMergeNode in="blur" />
              <feMergeNode in="SourceGraphic" />
            </feMerge>
          </filter>
        </defs>
        {data.edges.map((edge) => {
          const source = positions.get(edge.source)
          const target = positions.get(edge.target)
          if (!source || !target) return null
          const active = !hovered || (neighbourIDs.has(edge.source) && neighbourIDs.has(edge.target))
          const middleX = (source.x + target.x) / 2
          const middleY = (source.y + target.y) / 2
          const dx = target.x - source.x
          const dy = target.y - source.y
          const length = Math.max(1, Math.hypot(dx, dy))
          const curveX = middleX - (dy / length) * Math.min(18, length * 0.08)
          const curveY = middleY + (dx / length) * Math.min(18, length * 0.08)
          return (
            <path
              key={`${edge.source}-${edge.target}`}
              d={`M ${source.x} ${source.y} Q ${curveX} ${curveY} ${target.x} ${target.y}`}
              fill="none"
              stroke="currentColor"
              className="text-primary"
              strokeWidth={1 + (edge.weight / maxWeight) * 3}
              strokeLinecap="round"
              opacity={active ? 0.2 + (edge.weight / maxWeight) * 0.35 : 0.04}
            >
              <title>{`Совместная встречаемость: ${edge.weight}`}</title>
            </path>
          )
        })}
        {data.nodes.map((node) => {
          const position = positions.get(node.id)!
          const radius = 8 + (node.count / maxCount) * 10
          const style = TYPE_STYLE[node.type] ?? { color: "#a78bfa", label: node.type }
          const dimmed = hovered !== null && !neighbourIDs.has(node.id)
          return (
            <g
              key={node.id}
              opacity={dimmed ? 0.35 : 1}
              onMouseEnter={() => setHovered(node.id)}
              onMouseLeave={() => setHovered(null)}
              style={{ cursor: "pointer", transition: "opacity 150ms ease" }}
            >
              <circle
                cx={position.x}
                cy={position.y}
                r={radius + 4}
                fill={style.color}
                opacity={hovered === node.id ? 0.3 : 0.12}
              />
              <circle
                cx={position.x}
                cy={position.y}
                r={radius}
                fill={style.color}
                stroke="rgba(255,255,255,0.55)"
                strokeWidth={hovered === node.id ? 2 : 1}
                filter={hovered === node.id ? `url(#${glowID})` : undefined}
              >
                <title>{`${node.label}: ${node.count} исследований`}</title>
              </circle>
              <text
                x={position.x}
                y={position.y + 3}
                textAnchor="middle"
                className="fill-slate-950 text-[9px] font-semibold"
                pointerEvents="none"
              >
                {node.count}
              </text>
              <text
                x={position.x}
                y={position.y + radius + 14}
                textAnchor="middle"
                className="fill-foreground text-[10px]"
                stroke="hsl(var(--background))"
                strokeWidth={3}
                paintOrder="stroke"
                pointerEvents="none"
              >
                {labelOf(node.label)}
              </text>
            </g>
          )
        })}
      </svg>
      <p className="text-xs text-muted-foreground">
        Размер узла - частота в исследованиях, толщина линии - совместная встречаемость. Наведите
        на узел, чтобы подсветить его связи.
      </p>
    </div>
  )
}
