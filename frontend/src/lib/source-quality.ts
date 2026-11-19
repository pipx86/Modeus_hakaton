export interface SourceQualityItem {
  id: string
  type: string
  title: string
  content_chars: number
  created_at: string
  content_present: boolean
  origin_present: boolean
  url_format: string
  citations: number
  quote_checked: number
  quote_matched: number
}

export interface SourceScore {
  total: number
  authority: number
  freshness: number
  volume: number
  extraction: number
  origin: number
  citations: number
  quotes: number
}

const AUTHORITY: Record<string, number> = {
  standard: 20,
  document: 16,
  vacancy: 14,
  interview: 12,
}

function freshnessScore(createdAt: string, now: number): number {
  const time = new Date(createdAt).getTime()
  if (!Number.isFinite(time)) return 6
  const days = Math.max(0, (now - time) / 86_400_000)
  if (days <= 30) return 15
  if (days <= 90) return 12
  if (days <= 180) return 9
  if (days <= 365) return 5
  return 2
}

function volumeScore(chars: number): number {
  if (chars >= 5000) return 15
  if (chars >= 2000) return 12
  if (chars >= 500) return 8
  if (chars > 0) return 4
  return 0
}

function citationScore(citations: number): number {
  if (citations >= 5) return 20
  if (citations >= 2) return 14
  if (citations >= 1) return 8
  return 0
}

function quoteScore(checked: number, matched: number): number {
  if (checked === 0) return 5
  const ratio = matched / checked
  if (ratio >= 0.9) return 10
  if (ratio >= 0.6) return 7
  if (ratio > 0) return 3
  return 0
}

/** Оценка источника по 7 прозрачным критериям, максимум 100 баллов. */
export function scoreSource(item: SourceQualityItem, now = Date.now()): SourceScore {
  const authority = AUTHORITY[item.type] ?? 10
  const freshness = freshnessScore(item.created_at, now)
  const volume = volumeScore(item.content_chars)
  const extraction = item.content_present ? 10 : 0
  const origin =
    (item.origin_present ? 6 : 0) +
    (item.url_format === "valid" ? 4 : 0)
  const citations = citationScore(item.citations)
  const quotes = quoteScore(item.quote_checked, item.quote_matched)
  return {
    total: authority + freshness + volume + extraction + origin + citations + quotes,
    authority,
    freshness,
    volume,
    extraction,
    origin,
    citations,
    quotes,
  }
}

export function scoreTone(total: number): "success" | "warning" | "destructive" {
  if (total >= 80) return "success"
  if (total >= 60) return "warning"
  return "destructive"
}
