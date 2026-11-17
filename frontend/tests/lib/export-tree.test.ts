import { describe, expect, it } from "vitest"
import { treeToJson, treeToMarkdown } from "@/lib/export-tree"
import type { ActivityNode } from "@/lib/api/client"

const nodes: ActivityNode[] = [
  { id: "r", type: "role", title: "Data Scientist", evidence: [] },
  { id: "f", type: "function", title: "Анализ метрик", parent_id: "r", evidence: [] },
  {
    id: "a",
    type: "action",
    title: "Сводит отчёт в SQL",
    parent_id: "f",
    evidence: [],
    zun: { knowledge: ["SQL"], skills: [], abilities: [] },
  },
]

describe("экспорт дерева", () => {
  it("собирает Markdown с вложенностью и типами", () => {
    const markdown = treeToMarkdown(nodes)

    expect(markdown).toContain("- Data Scientist _(Роль)_")
    expect(markdown).toContain("  - Анализ метрик _(Функция)_")
    expect(markdown).toContain("    - Сводит отчёт в SQL _(Действие)_")
  })

  it("собирает JSON с детьми и ЗУН", () => {
    const parsed = JSON.parse(treeToJson(nodes)) as Array<{
      title: string
      children: Array<{ title: string; children: Array<{ zun: unknown }> }>
    }>

    expect(parsed).toHaveLength(1)
    expect(parsed[0].title).toBe("Data Scientist")
    expect(parsed[0].children[0].children[0].zun).toEqual({
      knowledge: ["SQL"],
      skills: [],
      abilities: [],
    })
  })
})
