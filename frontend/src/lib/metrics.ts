/**
 * @file metrics.ts
 * @description Минимальный парсер Prometheus-текста и хелперы для админки:
 * сумма счётчиков по фильтру и средняя латентность из гистограммы.
 */

export interface MetricSample {
  name: string
  labels: Record<string, string>
  value: number
}

const LINE_PATTERN = /^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{([^}]*)\})?\s+(-?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?)$/

export function parseMetrics(text: string): MetricSample[] {
  const samples: MetricSample[] = []
  for (const raw of text.split("\n")) {
    const line = raw.trim()
    if (!line || line.startsWith("#")) continue
    const match = LINE_PATTERN.exec(line)
    if (!match) continue
    const [, name, labelText, value] = match
    const labels: Record<string, string> = {}
    if (labelText) {
      for (const part of labelText.split(",")) {
        const eq = part.indexOf("=")
        if (eq === -1) continue
        const key = part.slice(0, eq).trim()
        let val = part.slice(eq + 1).trim()
        if (val.startsWith('"') && val.endsWith('"')) val = val.slice(1, -1)
        labels[key] = val.replace(/\\"/g, '"')
      }
    }
    samples.push({ name, labels, value: Number(value) })
  }
  return samples
}

export function sumWhere(
  samples: MetricSample[],
  name: string,
  predicate?: (sample: MetricSample) => boolean,
): number {
  return samples
    .filter((sample) => sample.name === name && (!predicate || predicate(sample)))
    .reduce((total, sample) => total + sample.value, 0)
}

/** Средняя латентность из пары `_sum`/`_count` в миллисекундах. */
export function averageHistogramMs(
  samples: MetricSample[],
  base: string,
  predicate?: (sample: MetricSample) => boolean,
): number | null {
  const matched = samples.filter((sample) => !predicate || predicate(sample))
  const sum = matched
    .filter((sample) => sample.name === `${base}_sum`)
    .reduce((total, sample) => total + sample.value, 0)
  const count = matched
    .filter((sample) => sample.name === `${base}_count`)
    .reduce((total, sample) => total + sample.value, 0)
  if (count === 0) return null
  return (sum / count) * 1000
}

/** URL метрик: в dev проксируется, при VITE_API_URL — абсолютный адрес бэкенда. */
export function metricsEndpoint(): string {
  const base = import.meta.env.VITE_API_URL
  return base ? `${base}/metrics` : "/metrics"
}

export async function fetchMetricsText(): Promise<string> {
  const response = await fetch(metricsEndpoint())
  if (!response.ok) throw new Error(`metrics: ${response.status}`)
  return response.text()
}
