import { describe, expect, it } from "vitest"
import type { ActivityNode } from "@/lib/api/client"
import { buildTreeMatrix } from "@/lib/tree-matrix"

const nodes: ActivityNode[] = [
  { id: "role", type: "role", title: "Backend", evidence: [] },
  {
    id: "fn",
    type: "function",
    title: "Разработка",
    parent_id: "role",
    evidence: [{ id: "e1", source_id: "s1", quote: "q1" }],
  },
  {
    id: "task-1",
    type: "task",
    title: "Писать код",
    parent_id: "fn",
    evidence: [{ id: "e2", source_id: "s2", quote: "q2" }],
  },
  {
    id: "task-2",
    type: "task",
    title: "Ревью",
    parent_id: "fn",
    evidence: [{ id: "e3", source_id: "s2", quote: "q3" }],
  },
]

describe("buildTreeMatrix", () => {
  it("строит матрицу без профессии", () => {
    const data = buildTreeMatrix(nodes)
    expect(data.nodes.map((node) => node.id)).toEqual(["fn", "task-1", "task-2"])
    expect(data.matrix).toEqual([
      [4, 3, 3],
      [3, 4, 2],
      [3, 2, 4],
    ])
  })

  it("ограничивает матрицу и сообщает об усечении", () => {
    const big = Array.from({ length: 30 }, (_, index) => ({
      id: `node-${index}`,
      type: "task" as const,
      title: `Задача ${index}`,
      parent_id: "node-0",
      evidence: [],
    }))
    const data = buildTreeMatrix(big, 14)
    expect(data.nodes).toHaveLength(14)
    expect(data.truncated).toBe(16)
  })
})
