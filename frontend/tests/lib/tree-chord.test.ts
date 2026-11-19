import { describe, expect, it } from "vitest"
import type { ActivityNode } from "@/lib/api/client"
import { buildTreeChord } from "@/lib/tree-chord"

const nodes: ActivityNode[] = [
  { id: "role-1", type: "role", title: "Backend", evidence: [] },
  { id: "fn-1", type: "function", title: "Тестирование", parent_id: "role-1", evidence: [] },
  { id: "task-1", type: "task", title: "Писать тесты", parent_id: "fn-1", evidence: [] },
]

describe("buildTreeChord", () => {
  it("строит узлы и связи родитель → ребёнок", () => {
    const { data, truncated } = buildTreeChord(nodes)
    expect(truncated).toBe(0)
    expect(data.nodes).toHaveLength(3)
    expect(data.edges).toEqual([
      { source: "role-1", target: "fn-1", weight: 1 },
      { source: "fn-1", target: "task-1", weight: 1 },
    ])
    expect(data.sample_size).toBe(3)
  })

  it("ограничивает большие деревья и сообщает об усечении", () => {
    const big = Array.from({ length: 40 }, (_, index) => ({
      id: `node-${index}`,
      type: "task" as const,
      title: `Задача ${index}`,
      parent_id: index === 0 ? undefined : "node-0",
      evidence: [],
    }))
    const { data, truncated } = buildTreeChord(big, 24)
    expect(data.nodes).toHaveLength(24)
    expect(truncated).toBe(16)
  })
})
