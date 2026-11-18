import { describe, expect, it } from "vitest"
import type { SnapshotDiff } from "@/lib/api/client"
import { buildSnapshotStory } from "@/lib/snapshot-story"

const node = (id: string, title: string) => ({ id, type: "task" as const, title, evidence: [] })

const diff: SnapshotDiff = {
  run_id: "r1",
  base: { id: "s1", version: 1 },
  head: { id: "s2", version: 2 },
  added: [node("n2", "Новая задача")],
  removed: [node("n1", "Старая задача")],
  changed: [
    {
      id: "n3",
      before: node("n3", "Старое имя"),
      after: node("n3", "Новое имя"),
      fields: ["title", "description"],
    },
  ],
  summary: { added: 1, removed: 1, changed: 1 },
}

describe("buildSnapshotStory", () => {
  it("собирает человеческий changelog с цифрами diff", () => {
    const story = buildSnapshotStory(diff)

    expect(story).toContain("v1 → v2")
    expect(story).toContain("+1 добавлено, -1 удалено, ~1 изменено")
    expect(story).toContain("Добавлены:")
    expect(story).toContain("«Новая задача»")
    expect(story).toContain("Удалены:")
    expect(story).toContain("«Старая задача»")
    expect(story).toContain("«Старое имя» → «Новое имя»")
    expect(story).toContain("название, описание")
  })

  it("пишет про совпадение при пустом diff", () => {
    const empty: SnapshotDiff = {
      ...diff,
      added: [],
      removed: [],
      changed: [],
      summary: { added: 0, removed: 0, changed: 0 },
    }
    expect(buildSnapshotStory(empty)).toContain("Снимки совпадают")
  })
})
