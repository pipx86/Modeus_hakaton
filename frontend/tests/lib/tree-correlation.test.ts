import { describe, expect, it } from "vitest"
import type { ActivityNode } from "@/lib/api/client"
import { buildTreeCorrelation } from "@/lib/tree-correlation"

const nodes: ActivityNode[] = [
  { id: "role", type: "role", title: "Backend", evidence: [] },
  {
    id: "fn",
    type: "function",
    title: "Разработка",
    parent_id: "role",
    evidence: [
      { id: "e1", source_id: "s1", quote: "q1" },
      { id: "e2", source_id: "s2", quote: "q2" },
    ],
  },
  {
    id: "task-1",
    type: "task",
    title: "Писать код",
    parent_id: "fn",
    evidence: [{ id: "e3", source_id: "s1", quote: "q3" }],
  },
  {
    id: "task-2",
    type: "task",
    title: "Ревью",
    parent_id: "fn",
    evidence: [],
  },
]

describe("buildTreeCorrelation", () => {
  it("строит матрицу по узлам без профессии", () => {
    const data = buildTreeCorrelation(nodes)
    expect(data.sample_size).toBe(3)
    expect(data.metrics.map((metric) => metric.key)).toEqual([
      "evidence",
      "sources",
      "children",
      "depth",
      "subtree",
    ])
    expect(data.matrix).toHaveLength(5)
    data.matrix.forEach((row, index) => {
      expect(row).toHaveLength(5)
      expect(row[index]).toBe(1)
    })
  })

  it("возвращает нулевую корреляцию для константной метрики", () => {
    const data = buildTreeCorrelation([
      { id: "task-1", type: "task", title: "A", evidence: [] },
      { id: "task-2", type: "task", title: "B", evidence: [] },
    ])
    expect(data.sample_size).toBe(2)
    expect(data.matrix[0][1]).toBe(0)
  })
})
