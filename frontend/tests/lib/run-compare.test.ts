import { describe, expect, it } from "vitest"
import type { ActivityNode } from "@/lib/api/client"
import { diffRunTrees } from "@/lib/run-compare"

const base: ActivityNode[] = [
  { id: "r1", type: "role", title: "Аналитик", evidence: [] },
  { id: "f1", type: "function", title: "Отчётность", parent_id: "r1", evidence: [] },
  { id: "t1", type: "task", title: "Старая задача", parent_id: "f1", evidence: [] },
]

const head: ActivityNode[] = [
  { id: "r1", type: "role", title: "Аналитик данных", evidence: [] },
  { id: "f1", type: "function", title: "Отчётность", parent_id: "r1", evidence: [] },
  { id: "t2", type: "task", title: "Новая задача", parent_id: "f1", evidence: [] },
]

describe("diffRunTrees", () => {
  it("находит добавленные, удалённые и изменённые узлы", () => {
    const diff = diffRunTrees(base, head)

    expect(diff.summary).toEqual({ added: 1, removed: 1, changed: 1 })
    expect(diff.added[0].title).toBe("Новая задача")
    expect(diff.removed[0].title).toBe("Старая задача")
    expect(diff.changed[0].fields).toEqual(["title"])
  })

  it("возвращает пустой diff для одинаковых деревьев", () => {
    const diff = diffRunTrees(base, base)
    expect(diff.summary).toEqual({ added: 0, removed: 0, changed: 0 })
  })
})
