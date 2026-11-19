import type { ActivityNode, SkillGraphResponse } from "@/lib/api/client"

export interface TreeChordData {
  data: SkillGraphResponse
  truncated: number
}

/**
 * Строит хордовую карту по конкретному дереву: узлы по уровням, хорды -
 * связи «родитель → дочерний узел». Для больших деревьев берутся первые
 * уровни, чтобы подписи оставались читаемыми.
 */
export function buildTreeChord(nodes: ActivityNode[], limit = 24): TreeChordData {
  const byID = new Map(nodes.map((node) => [node.id, node]))
  const children = new Map<string, ActivityNode[]>()
  const roots: ActivityNode[] = []
  for (const node of nodes) {
    if (!node.parent_id || !byID.has(node.parent_id)) {
      roots.push(node)
      continue
    }
    const list = children.get(node.parent_id) ?? []
    list.push(node)
    children.set(node.parent_id, list)
  }

  const ordered: ActivityNode[] = []
  const queue = roots.length > 0 ? [...roots] : [...nodes]
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

  const chordNodes = ordered.map((node) => ({
    id: node.id,
    label: node.title,
    type: node.type,
    count: node.evidence?.length ?? 0,
  }))
  const included = new Set(chordNodes.map((node) => node.id))
  const edges = ordered
    .filter((node) => node.parent_id && included.has(node.parent_id))
    .map((node) => ({
      source: node.parent_id as string,
      target: node.id,
      weight: 1,
    }))

  return {
    data: {
      sample_size: nodes.length,
      nodes: chordNodes,
      edges,
    },
    truncated: Math.max(0, nodes.length - ordered.length),
  }
}
