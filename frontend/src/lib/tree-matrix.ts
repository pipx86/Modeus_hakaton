import type { ActivityNode } from "@/lib/api/client"

export interface TreeMatrixData {
  nodes: ActivityNode[]
  matrix: number[][]
  truncated: number
}

const RELATION_SELF = 4
const RELATION_PARENT = 3
const RELATION_SIBLING = 2
const RELATION_SOURCE = 1

function orderedNodes(nodes: ActivityNode[], limit: number): ActivityNode[] {
  const visible = nodes.filter((node) => node.type !== "role")
  const byID = new Map(visible.map((node) => [node.id, node]))
  const children = new Map<string, ActivityNode[]>()
  const roots: ActivityNode[] = []
  for (const node of visible) {
    if (!node.parent_id || !byID.has(node.parent_id)) {
      roots.push(node)
      continue
    }
    const list = children.get(node.parent_id) ?? []
    list.push(node)
    children.set(node.parent_id, list)
  }
  const queue = roots.length > 0 ? [...roots] : [...visible]
  const ordered: ActivityNode[] = []
  const seen = new Set<string>()
  while (queue.length > 0 && ordered.length < limit) {
    const node = queue.shift()!
    if (seen.has(node.id)) continue
    seen.add(node.id)
    ordered.push(node)
    for (const child of children.get(node.id) ?? []) {
      if (ordered.length + queue.length >= limit) break
      queue.push(child)
    }
  }
  return ordered
}

function sourceIDs(node: ActivityNode): Set<string> {
  return new Set((node.evidence ?? []).map((item) => item.source_id).filter(Boolean))
}

/** Матрица совместной встречаемости узлов дерева. */
export function buildTreeMatrix(nodes: ActivityNode[], limit = 12): TreeMatrixData {
  const ordered = orderedNodes(nodes, limit)
  const included = new Set(ordered.map((node) => node.id))
  const sources = new Map(ordered.map((node) => [node.id, sourceIDs(node)]))
  const matrix = ordered.map((left) =>
    ordered.map((right) => {
      if (left.id === right.id) return RELATION_SELF
      if (left.parent_id === right.id || right.parent_id === left.id) {
        return RELATION_PARENT
      }
      if (
        left.parent_id &&
        left.parent_id === right.parent_id &&
        included.has(left.parent_id)
      ) {
        return RELATION_SIBLING
      }
      const shared = [...sources.get(left.id)!].some((id) => sources.get(right.id)!.has(id))
      return shared ? RELATION_SOURCE : 0
    }),
  )
  return {
    nodes: ordered,
    matrix,
    truncated: Math.max(0, nodes.filter((node) => node.type !== "role").length - ordered.length),
  }
}

export const TREE_MATRIX_LEGEND = [
  { value: RELATION_SELF, label: "узел", color: "hsl(var(--primary))" },
  { value: RELATION_PARENT, label: "родитель ↔ ребёнок", color: "hsl(var(--success))" },
  { value: RELATION_SIBLING, label: "соседи", color: "hsl(var(--warning))" },
  { value: RELATION_SOURCE, label: "общий источник", color: "hsl(var(--primary) / 0.35)" },
] as const
