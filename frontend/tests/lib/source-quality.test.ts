import { describe, expect, it } from "vitest"
import { scoreSource, scoreTone, type SourceQualityItem } from "@/lib/source-quality"

const now = new Date("2026-11-19T12:00:00Z").getTime()

function source(overrides: Partial<SourceQualityItem>): SourceQualityItem {
  return {
    id: "s-1",
    type: "standard",
    title: "Профстандарт",
    content_chars: 6000,
    created_at: "2026-11-01T00:00:00Z",
    content_present: true,
    origin_present: true,
    url_format: "valid",
    citations: 5,
    quote_checked: 5,
    quote_matched: 5,
    ...overrides,
  }
}

describe("scoreSource", () => {
  it("даёт 100 баллов сильному источнику", () => {
    expect(scoreSource(source({}), now).total).toBe(100)
  })

  it("снижает оценку за старый, пустой и нецитируемый источник", () => {
    const score = scoreSource(
      source({
        type: "interview",
        content_chars: 0,
        created_at: "2024-01-01T00:00:00Z",
        content_present: false,
        origin_present: false,
        url_format: "missing",
        citations: 0,
        quote_checked: 0,
        quote_matched: 0,
      }),
      now,
    )
    expect(score.total).toBe(19)
    expect(score.freshness).toBe(2)
    expect(score.quotes).toBe(5)
  })

  it("считает подтверждение цитат", () => {
    const score = scoreSource(
      source({ quote_checked: 10, quote_matched: 4, citations: 1 }),
      now,
    )
    expect(score.quotes).toBe(3)
    expect(score.citations).toBe(8)
  })

  it("подбирает тон оценки", () => {
    expect(scoreTone(85)).toBe("success")
    expect(scoreTone(65)).toBe("warning")
    expect(scoreTone(40)).toBe("destructive")
  })
})
