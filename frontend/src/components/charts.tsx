/**
 * @file charts.tsx
 * @description Лёгкие SVG/div-визуализации без внешних библиотек:
 * столбики с разбивкой, donut и спарклайн.
 */

import { cn } from "@/lib/utils"

export interface BarSegment {
  value: number
  className: string
}

export interface BarDatum {
  label: string
  segments: BarSegment[]
}

/** Столбики по дням: сегменты стека, высота пропорциональна максимуму. */
export function StackedBars({ data, className }: { data: BarDatum[]; className?: string }) {
  const totals = data.map((item) => item.segments.reduce((sum, segment) => sum + segment.value, 0))
  const max = Math.max(1, ...totals)

  return (
    <div className={cn("flex items-end gap-1.5", className)}>
      {data.map((item, index) => {
        const total = totals[index]
        return (
          <div
            key={item.label}
            className="flex min-w-0 flex-1 flex-col items-center gap-1"
            title={`${item.label}: ${total}`}
          >
            <div className="flex h-24 w-full items-end">
              <div
                className="flex w-full flex-col-reverse overflow-hidden rounded-sm"
                style={{ height: `${Math.max(total > 0 ? 4 : 0, (total / max) * 100)}%` }}
              >
                {item.segments.map((segment, segmentIndex) =>
                  segment.value > 0 ? (
                    <div
                      key={segmentIndex}
                      className={segment.className}
                      style={{ height: `${(segment.value / total) * 100}%` }}
                    />
                  ) : null,
                )}
              </div>
            </div>
            <span className="truncate text-[10px] text-muted-foreground">{item.label}</span>
          </div>
        )
      })}
    </div>
  )
}

export interface DonutSegment {
  label: string
  value: number
  className: string
}

/** Кольцевая диаграмма с подписью в центре. */
export function Donut({
  segments,
  size = 148,
  thickness = 16,
  centerLabel,
  centerValue,
}: {
  segments: DonutSegment[]
  size?: number
  thickness?: number
  centerLabel?: string
  centerValue?: string
}) {
  const total = segments.reduce((sum, segment) => sum + segment.value, 0)
  const radius = (size - thickness) / 2
  const circumference = 2 * Math.PI * radius
  let offset = 0

  return (
    <div className="relative inline-flex" style={{ width: size, height: size }}>
      <svg viewBox={`0 0 ${size} ${size}`} className="-rotate-90">
        <circle
          cx={size / 2}
          cy={size / 2}
          r={radius}
          fill="none"
          stroke="currentColor"
          strokeWidth={thickness}
          className="text-muted/40"
        />
        {total > 0 &&
          segments.map((segment) => {
            const length = (segment.value / total) * circumference
            const circle = (
              <circle
                key={segment.label}
                cx={size / 2}
                cy={size / 2}
                r={radius}
                fill="none"
                stroke="currentColor"
                strokeWidth={thickness}
                strokeDasharray={`${length} ${circumference - length}`}
                strokeDashoffset={-offset}
                className={segment.className}
              />
            )
            offset += length
            return circle
          })}
      </svg>
      <div className="absolute inset-0 flex flex-col items-center justify-center">
        <span className="text-2xl font-semibold">{centerValue ?? total}</span>
        {centerLabel && <span className="text-xs text-muted-foreground">{centerLabel}</span>}
      </div>
    </div>
  )
}

/** Спарклайн: линия по значениям, без осей. */
export function Sparkline({
  values,
  className,
  width = 320,
  height = 64,
}: {
  values: number[]
  className?: string
  width?: number
  height?: number
}) {
  if (values.length < 2) {
    return (
      <div className="flex h-16 items-center text-xs text-muted-foreground">Недостаточно данных</div>
    )
  }
  const max = Math.max(...values)
  const min = Math.min(...values)
  const range = max - min || 1
  const step = width / (values.length - 1)
  const points = values
    .map((value, index) => `${(index * step).toFixed(1)},${(height - ((value - min) / range) * height).toFixed(1)}`)
    .join(" ")
  const area = `0,${height} ${points} ${width},${height}`

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      className={cn("h-16 w-full", className)}
      role="img"
      aria-label="график латентности"
    >
      <polygon points={area} fill="currentColor" opacity={0.12} />
      <polyline
        points={points}
        fill="none"
        stroke="currentColor"
        strokeWidth={2}
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  )
}
