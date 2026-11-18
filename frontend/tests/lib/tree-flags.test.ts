import { describe, expect, it } from "vitest"
import type { ActivityNode, Source } from "@/lib/api/client"
import { computeTreeFlags } from "@/lib/tree-flags"
import { buildSourceIndex } from "@/lib/provenance"

const sources: Source[] = [
  { id: "s-int", type: "interview", title: "Интервью" },
  { id: "s-doc", type: "document", title: "Документ" },
]

const nodes: ActivityNode[] = [
  { id: "r1", type: "role", title: "Аналитик", evidence: [{ id: "e1", source_id: "s-int", quote: "q" }] },
  {
    id: "f1",
    type: "function",
    title: "Аналитика",
    parent_id: "r1",
    evidence: [{ id: "e2", source_id: "s-doc", quote: "q" }],
  },
  {
    id: "t1",
    type: "task",
    title: "Аналитика",
    parent_id: "f1",
    evidence: [{ id: "e3", source_id: "s-int", quote: "q" }],
  },
  {
    id: "a1",
    type: "action",
    title: "Разное",
    parent_id: "t1",
    evidence: [],
  },
  {
    id: "f2",
    type: "function",
    title: "Пустая функция",
    parent_id: "r1",
    evidence: [],
  },
]

describe("computeTreeFlags", () => {
  it("находит общее название", () => {
    const flags = computeTreeFlags(nodes)
    expect(flags.get("a1")?.map((flag) => flag.code)).toContain("generic_title")
  })

  it("находит дубли названий", () => {
    const flags = computeTreeFlags(nodes)
    expect(flags.get("f1")?.map((flag) => flag.code)).toContain("duplicate_title")
    expect(flags.get("t1")?.map((flag) => flag.code)).toContain("duplicate_title")
  })

  it("находит промежуточный узел без детей", () => {
    const flags = computeTreeFlags(nodes)
    expect(flags.get("f2")?.map((flag) => flag.code)).toContain("no_children")
  })

  it("находит узел без обоснований", () => {
    const flags = computeTreeFlags(nodes)
    expect(flags.get("f2")?.map((flag) => flag.code)).toContain("no_evidence")
  })

  it("находит единственный слабый источник", () => {
    const flags = computeTreeFlags(nodes, buildSourceIndex(sources))
    expect(flags.get("f1")?.map((flag) => flag.code)).toContain("weak_source")
  })
})
