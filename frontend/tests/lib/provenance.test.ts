import { describe, expect, it } from "vitest"
import type { Evidence, Source } from "@/lib/api/client"
import {
  SOURCE_PRIORITY,
  buildSourceIndex,
  explainNode,
  provenanceFor,
  provenanceLabel,
} from "@/lib/provenance"

const sources: Source[] = [
  { id: "s-int", type: "interview", title: "Интервью" },
  { id: "s-std", type: "standard", title: "Профстандарт" },
  { id: "s-vac", type: "vacancy", title: "Вакансия" },
]

const evidence = (sourceId: string, id = sourceId): Evidence => ({
  id,
  source_id: sourceId,
  quote: `Цитата ${id}`,
})

describe("provenance", () => {
  it("собирает уникальные типы источников в порядке приоритета", () => {
    const info = provenanceFor(
      [evidence("s-vac"), evidence("s-int", "e2"), evidence("s-std", "e3")],
      buildSourceIndex(sources),
    )

    expect(info.count).toBe(3)
    expect(info.types).toEqual(["interview", "standard", "vacancy"])
    expect(info.labels).toEqual(["Интервью", "Профстандарт", "Вакансия"])
    expect(provenanceLabel(info)).toBe("3 типа")
  })

  it("не падает без метаданных источников", () => {
    const info = provenanceFor([evidence("unknown")], new Map())
    expect(info.count).toBe(0)
    expect(info.unknown).toBe(1)
    expect(provenanceLabel(info)).toBe("источник")
  })

  it("объясняет узел через приоритет источников", () => {
    const text = explainNode("Проверять гипотезы", [evidence("s-int"), evidence("s-doc")], buildSourceIndex(sources))
    expect(text).toContain("Проверять гипотезы")
    expect(text).toContain("Интервью")
    expect(text).toContain(String(SOURCE_PRIORITY.interview))
  })

  it("объясняет узел без обоснований", () => {
    expect(explainNode("Пустой узел", [], new Map())).toContain("обоснований нет")
  })
})
