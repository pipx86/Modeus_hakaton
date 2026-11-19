import type { CSSProperties } from "react"
import type { CorrelationResponse } from "@/lib/api/client"

function cellStyle(value: number): CSSProperties {
  const intensity = Math.min(1, Math.abs(value))
  const alpha = 0.06 + intensity * 0.55
  return {
    backgroundColor:
      value >= 0 ? `rgba(16, 185, 129, ${alpha})` : `rgba(244, 63, 94, ${alpha})`,
  }
}

/** Тепловая карта коэффициентов Пирсона между метриками завершённых прогонов. */
export function CorrelationMap({ data }: { data: CorrelationResponse }) {
  if (data.metrics.length === 0 || data.matrix.length === 0) {
    return (
      <p className="rounded-lg border border-dashed p-4 text-sm text-muted-foreground">
        Для карты корреляции пока нет метрик.
      </p>
    )
  }
  if (data.sample_size < 2) {
    return (
      <p className="rounded-lg border border-dashed p-4 text-sm text-muted-foreground">
        Нужно минимум два завершённых исследования с деревом. Сейчас: {data.sample_size}.
      </p>
    )
  }
  return (
    <div className="space-y-3">
      <div className="overflow-x-auto">
        <table className="min-w-[640px] border-separate border-spacing-1 text-xs">
          <thead>
            <tr>
              <th className="w-44 text-left font-medium text-muted-foreground" />
              {data.metrics.map((metric) => (
                <th
                  key={metric.key}
                  className="px-1 pb-1 text-center font-medium text-muted-foreground"
                  title={metric.label}
                >
                  {metric.label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {data.metrics.map((rowMetric, rowIndex) => (
              <tr key={rowMetric.key}>
                <th className="pr-2 text-left font-medium text-muted-foreground" title={rowMetric.label}>
                  {rowMetric.label}
                </th>
                {data.metrics.map((columnMetric, columnIndex) => {
                  const value = data.matrix[rowIndex]?.[columnIndex] ?? 0
                  return (
                    <td
                      key={columnMetric.key}
                      className="rounded-md px-2 py-2 text-center font-medium tabular-nums"
                      style={cellStyle(value)}
                      title={`${rowMetric.label} × ${columnMetric.label}: ${value.toFixed(2)}`}
                    >
                      {value.toFixed(2)}
                    </td>
                  )
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span>−1</span>
        <span className="h-2 w-32 rounded-full bg-gradient-to-r from-rose-500/70 via-muted to-emerald-500/70" />
        <span>+1</span>
        <span>· исследования: {data.sample_size}</span>
      </div>
    </div>
  )
}
