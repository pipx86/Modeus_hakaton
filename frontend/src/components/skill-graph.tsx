import { useId, useMemo, useState } from "react"
import type { SkillGraphResponse } from "@/lib/api/client"

const VIEW_WIDTH = 760
const VIEW_HEIGHT = 470
const CENTER_X = VIEW_WIDTH / 2
const CENTER_Y = VIEW_HEIGHT / 2
const RADIUS = 152
const LABEL_RADIUS = RADIUS + 38

const TYPE_STYLE: Record<string, { color: string; label: string }> = {
  role: { color: "#a78bfa", label: "Роли" },
  function: { color: "#38bdf8", label: "Функции" },
  task: { color: "#f59e0b", label: "Задачи" },
  action: { color: "#34d399", label: "Действия" },
}

type Node = SkillGraphResponse["nodes"][number]
type Filter = "all" | "role" | "function" | "task" | "action"

interface Slice {
  node: Node
  start: number
  end: number
  mid: number
}

function polar(angle: number, radius: number) {
  return {
    x: CENTER_X + Math.cos(angle) * radius,
    y: CENTER_Y + Math.sin(angle) * radius,
  }
}

function arcPath(start: number, end: number, radius: number): string {
  const from = polar(start, radius)
  const to = polar(end, radius)
  const large = end - start > Math.PI ? 1 : 0
  return `M ${from.x} ${from.y} A ${radius} ${radius} 0 ${large} 1 ${to.x} ${to.y}`
}

function shortLabel(label: string): string {
  const value = label.trim()
  if (value.length === 0) return ""
  return value.charAt(0).toUpperCase() + value.slice(1)
}

function truncate(label: string, max = 22): string {
  return label.length > max ? `${label.slice(0, max - 1)}…` : label
}

function buildSlices(nodes: Node[]): Slice[] {
  const gap = nodes.length > 1 ? 0.035 : 0
  const available = Math.PI * 2 - gap * nodes.length
  const total = Math.max(
    1,
    nodes.reduce((sum, node) => sum + Math.max(1, node.count), 0),
  )
  let cursor = -Math.PI / 2
  return nodes.map((node) => {
    const span = (available * Math.max(1, node.count)) / total
    const slice = { node, start: cursor, end: cursor + span, mid: cursor + span / 2 }
    cursor += span + gap
    return slice
  })
}

/** Хордовая диаграмма связей: дуги по типам, хорды по связям узлов. */
export function SkillGraph({
  data,
  sampleLabel = "Исследований",
  nodeUnit = "иссл.",
  edgeUnit = "Совместная встречаемость",
  description,
}: {
  data: SkillGraphResponse
  sampleLabel?: string
  nodeUnit?: string
  edgeUnit?: string
  description?: string
}) {
  const glowID = useId().replace(/:/g, "")
  const [hovered, setHovered] = useState<string | null>(null)
  const [filter, setFilter] = useState<Filter>("all")

  const visibleNodes = useMemo(
    () => (filter === "all" ? data.nodes : data.nodes.filter((node) => node.type === filter)),
    [data.nodes, filter],
  )
  const visibleIDs = useMemo(() => new Set(visibleNodes.map((node) => node.id)), [visibleNodes])
  const visibleEdges = useMemo(
    () => data.edges.filter((edge) => visibleIDs.has(edge.source) && visibleIDs.has(edge.target)),
    [data.edges, visibleIDs],
  )
  const slices = useMemo(() => buildSlices(visibleNodes), [visibleNodes])
  const sliceByID = useMemo(() => new Map(slices.map((slice) => [slice.node.id, slice])), [slices])

  const endpointAngles = useMemo(() => {
    const map = new Map<string, { source: number; target: number }>()
    const incident = new Map<string, number[]>()
    visibleEdges.forEach((edge, index) => {
      incident.set(edge.source, [...(incident.get(edge.source) ?? []), index])
      incident.set(edge.target, [...(incident.get(edge.target) ?? []), index])
    })
    for (const slice of slices) {
      const indexes = incident.get(slice.node.id) ?? []
      indexes.forEach((edgeIndex, position) => {
        const fraction = indexes.length === 1 ? 0.5 : (position + 1) / (indexes.length + 1)
        const angle = slice.start + (slice.end - slice.start) * fraction
        const edge = visibleEdges[edgeIndex]
        const key = `${edgeIndex}`
        const current = map.get(key) ?? { source: 0, target: 0 }
        if (edge.source === slice.node.id) current.source = angle
        else current.target = angle
        map.set(key, current)
      })
    }
    return map
  }, [slices, visibleEdges])

  if (data.nodes.length === 0) {
    return (
      <p className="rounded-lg border border-dashed p-4 text-sm text-muted-foreground">
        Нужны завершённые исследования с функциями, задачами или действиями в дереве.
      </p>
    )
  }
  if (visibleNodes.length === 0) {
    return <p className="text-sm text-muted-foreground">В этой категории пока нет компетенций.</p>
  }

  const neighbours = new Set<string>()
  if (hovered) {
    neighbours.add(hovered)
    for (const edge of visibleEdges) {
      if (edge.source === hovered) neighbours.add(edge.target)
      if (edge.target === hovered) neighbours.add(edge.source)
    }
  }
  const maxWeight = Math.max(...visibleEdges.map((edge) => edge.weight), 1)

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <button
          type="button"
          onClick={() => setFilter("all")}
          className={`rounded-full border px-2.5 py-1 transition-colors ${
            filter === "all" ? "border-primary bg-primary/15 text-foreground" : "hover:text-foreground"
          }`}
        >
          Все: {data.nodes.length}
        </button>
        {Object.entries(TYPE_STYLE).map(([type, style]) => {
          const count = data.nodes.filter((node) => node.type === type).length
          if (count === 0) return null
          return (
            <button
              key={type}
              type="button"
              onClick={() => setFilter(type as Filter)}
              className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 transition-colors ${
                filter === type
                  ? "border-primary bg-primary/15 text-foreground"
                  : "hover:text-foreground"
              }`}
            >
              <span className="size-2 rounded-full" style={{ backgroundColor: style.color }} />
              {style.label}: {count}
            </button>
          )
        })}
        <span className="ml-auto">
          {sampleLabel}: {data.sample_size}
        </span>
      </div>

      <svg
        viewBox={`0 0 ${VIEW_WIDTH} ${VIEW_HEIGHT}`}
        className="h-auto w-full rounded-xl border border-border/60 bg-gradient-to-b from-muted/25 to-transparent"
        role="img"
        aria-label="Хордовая диаграмма связей компетенций"
      >
        <defs>
          <filter id={glowID} x="-50%" y="-50%" width="200%" height="200%">
            <feGaussianBlur stdDeviation="3.5" result="blur" />
            <feMerge>
              <feMergeNode in="blur" />
              <feMergeNode in="SourceGraphic" />
            </feMerge>
          </filter>
        </defs>

        <circle cx={CENTER_X} cy={CENTER_Y} r={RADIUS - 10} fill="none" stroke="currentColor" className="text-border/50" strokeDasharray="2 7" />

        {visibleEdges.map((edge, index) => {
          const sourceSlice = sliceByID.get(edge.source)
          const targetSlice = sliceByID.get(edge.target)
          if (!sourceSlice || !targetSlice) return null
          const angles = endpointAngles.get(`${index}`)
          if (!angles) return null
          const source = polar(angles.source, RADIUS - 4)
          const target = polar(angles.target, RADIUS - 4)
          const sourceColor = TYPE_STYLE[sourceSlice.node.type]?.color ?? "#a78bfa"
          const targetColor = TYPE_STYLE[targetSlice.node.type]?.color ?? "#a78bfa"
          const active = !hovered || (neighbours.has(edge.source) && neighbours.has(edge.target))
          const weight = 1 + (edge.weight / maxWeight) * 4
          const ribbon = `M ${source.x} ${source.y} Q ${CENTER_X} ${CENTER_Y} ${target.x} ${target.y} Q ${CENTER_X} ${CENTER_Y} ${source.x} ${source.y} Z`
          return (
            <g key={`${edge.source}-${edge.target}`} opacity={active ? 1 : 0.07}>
              <path
                d={ribbon}
                fill={sourceColor}
                opacity={0.08 + (edge.weight / maxWeight) * 0.16}
                pointerEvents="none"
              />
              <path
                d={`M ${source.x} ${source.y} Q ${CENTER_X} ${CENTER_Y} ${target.x} ${target.y}`}
                fill="none"
                stroke={sourceColor}
                strokeOpacity={0.36 + (edge.weight / maxWeight) * 0.45}
                strokeWidth={weight}
                strokeLinecap="round"
                pointerEvents="none"
              />
              <path
                d={`M ${source.x} ${source.y} Q ${CENTER_X} ${CENTER_Y} ${target.x} ${target.y}`}
                fill="none"
                stroke={targetColor}
                strokeOpacity={0.15}
                strokeWidth={weight + 2}
                strokeLinecap="round"
                pointerEvents="none"
              >
                <title>{`${edgeUnit}: ${edge.weight}`}</title>
              </path>
            </g>
          )
        })}

        {slices.map((slice) => {
          const color = TYPE_STYLE[slice.node.type]?.color ?? "#a78bfa"
          const dimmed = hovered !== null && !neighbours.has(slice.node.id)
          const selected = hovered === slice.node.id
          const label = truncate(shortLabel(slice.node.label), 17)
          const anchor = polar(slice.mid, RADIUS)
          const labelPoint = polar(slice.mid, LABEL_RADIUS)
          const connectorStart = polar(slice.mid, RADIUS + 6)
          const connectorEnd = polar(slice.mid, LABEL_RADIUS - 8)
          const textAnchor = Math.cos(slice.mid) > 0.25 ? "start" : Math.cos(slice.mid) < -0.25 ? "end" : "middle"
          return (
            <g
              key={slice.node.id}
              opacity={dimmed ? 0.25 : 1}
              onMouseEnter={() => setHovered(slice.node.id)}
              onMouseLeave={() => setHovered(null)}
              style={{ cursor: "pointer" }}
            >
              <path
                d={arcPath(slice.start, slice.end, RADIUS)}
                fill="none"
                stroke={color}
                strokeWidth={selected ? 13 : 9}
                strokeLinecap="round"
                filter={selected ? `url(#${glowID})` : undefined}
              >
                <title>{`${slice.node.label}: ${slice.node.count} исследований`}</title>
              </path>
              <line
                x1={connectorStart.x}
                y1={connectorStart.y}
                x2={connectorEnd.x}
                y2={connectorEnd.y}
                stroke={color}
                strokeOpacity={0.45}
              />
              <circle cx={anchor.x} cy={anchor.y} r={2.2} fill={color} />
              <text
                x={labelPoint.x}
                y={labelPoint.y - 2}
                textAnchor={textAnchor}
                className="fill-foreground text-[10px]"
                stroke="hsl(var(--background))"
                strokeWidth={4}
                paintOrder="stroke"
                pointerEvents="none"
              >
                {label}
              </text>
              <text
                x={labelPoint.x}
                y={labelPoint.y + 11}
                textAnchor={textAnchor}
                className="fill-muted-foreground text-[9px]"
                pointerEvents="none"
              >
                {slice.node.count} {nodeUnit}
              </text>
            </g>
          )
        })}
      </svg>
      <p className="text-xs text-muted-foreground">
        {description ??
          "Дуги - компетенции, хорды - совместная встречаемость. Наведите на дугу, чтобы выделить её связи; переключайте типы узлов сверху. Размер дуги зависит от частоты."}
      </p>
    </div>
  )
}
