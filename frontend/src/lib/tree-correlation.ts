import type { ActivityNode, CorrelationResponse } from "@/lib/api/client"

function correlation(left: number[], right: number[]): number {
  const count = left.length
  if (count < 2 || right.length !== count) return 0
  const leftMean = left.reduce((sum, value) => sum + value, 0) / count
  const rightMean = right.reduce((sum, value) => sum + value, 0) / count
  let covariance = 0
  let varianceLeft = 0
  let varianceRight = 0
  for (let index = 0; index < count; index += 1) {
    const dl = left[index] - leftMean
    const dr = right[index] - rightMean
    covariance += dl * dr
    varianceLeft += dl * dl
    varianceRight += dr * dr
  }
  if (varianceLeft === 0 || varianceRight === 0) return 0
  return Math.round((covariance / Math.sqrt(varianceLeft * varianceRight)) * 100) / 100
}

/** Матрица корреляции метрик по узлам одного дерева. */
export function buildTreeCorrelation(nodes: ActivityNode[]): CorrelationResponse {
  const visible = nodes.filter((node) => node.type !== "role")
  const byID = new Map(visible.map((node) => [node.id, node]))
  const children = new Map<string, string[]>()
  for (const node of visible) {
    if (!node.parent_id || !byID.has(node.parent_id)) continue
    const list = children.get(node.parent_id) ?? []
    list.push(node.id)
    children.set(node.parent_id, list)
  }

  const depthOf = (node: ActivityNode): number => {
    let depth = 0
    let current = node
    const seen = new Set<string>()
    while (current.parent_id && byID.has(current.parent_id) && !seen.has(current.id)) {
      seen.add(current.id)
      current = byID.get(current.parent_id)!
      depth += 1
    }
    return depth
  }

  const subtreeSize = (id: string): number => {
    let count = 0
    const stack = [...(children.get(id) ?? [])]
    const seen = new Set<string>()
    while (stack.length > 0) {
      const current = stack.pop()!
      if (seen.has(current)) continue
      seen.add(current)
      count += 1
      stack.push(...(children.get(current) ?? []))
    }
    return count
  }

  const metrics = [
    { key: "evidence", label: "Цитаты" },
    { key: "sources", label: "Источники" },
    { key: "children", label: "Дочерние" },
    { key: "depth", label: "Глубина" },
    { key: "subtree", label: "Поддерево" },
  ]

  const columns = [
    visible.map((node) => node.evidence?.length ?? 0),
    visible.map((node) => new Set((node.evidence ?? []).map((item) => item.source_id)).size),
    visible.map((node) => (children.get(node.id) ?? []).length),
    visible.map((node) => depthOf(node)),
    visible.map((node) => subtreeSize(node.id)),
  ]

  const matrix = metrics.map((_, row) =>
    metrics.map((__, column) => (row === column ? 1 : correlation(columns[row], columns[column]))),
  )

  return { sample_size: visible.length, metrics, matrix }
}
