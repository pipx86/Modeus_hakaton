/**
 * @file run-compare.ts
 * @description Сравнение деревьев двух завершённых прогонов: добавленные, удалённые и изменённые узлы.
 */

import type { ActivityNode } from "@/lib/api/client"

export interface RunNodeChange {
  before: ActivityNode
  after: ActivityNode
  fields: string[]
}

export interface RunTreeDiff {
  added: ActivityNode[]
  removed: ActivityNode[]
  changed: RunNodeChange[]
  summary: { added: number; removed: number; changed: number }
}

function comparable(node: ActivityNode): Record<string, unknown> {
  return {
    title: node.title,
    description: node.description ?? "",
    type: node.type,
    parent_id: node.parent_id ?? "",
    evidence: (node.evidence ?? []).map((item) => item.id).sort(),
  }
}

/** Сравнивает два дерева по id узлов; порядок сохраняется как в исходных списках. */
export function diffRunTrees(base: ActivityNode[], head: ActivityNode[]): RunTreeDiff {
  const baseById = new Map(base.map((node) => [node.id, node]))
  const headById = new Map(head.map((node) => [node.id, node]))

  const added = head.filter((node) => !baseById.has(node.id))
  const removed = base.filter((node) => !headById.has(node.id))
  const changed: RunNodeChange[] = []
  for (const node of head) {
    const before = baseById.get(node.id)
    if (!before) continue
    const previous = comparable(before)
    const current = comparable(node)
    const fields = Object.keys(previous).filter(
      (field) => JSON.stringify(previous[field]) !== JSON.stringify(current[field]),
    )
    if (fields.length > 0) changed.push({ before, after: node, fields })
  }

  return {
    added,
    removed,
    changed,
    summary: { added: added.length, removed: removed.length, changed: changed.length },
  }
}
