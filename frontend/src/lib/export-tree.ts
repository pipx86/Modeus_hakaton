/**
 * @file export-tree.ts
 * @description Экспорт дерева деятельности в Markdown/JSON и скачивание файла.
 */

import { buildTree, NODE_TYPE_LABELS, type TreeNode } from "@/components/activity-tree-model"
import type { ActivityNode } from "@/lib/api/client"

/**
 * Дерево в Markdown: вложенный список с типом узла и количеством обоснований.
 * Нужен, чтобы результат исследования можно было забрать из интерфейса.
 */
export function treeToMarkdown(nodes: ActivityNode[]): string {
  const lines: string[] = []
  const walk = (node: TreeNode, depth: number) => {
    const shift = "  ".repeat(depth)
    const evidence = (node.evidence ?? []).length
    const suffix = evidence > 0 ? ` - обоснований: ${evidence}` : ""
    lines.push(`${shift}- ${node.title} _(${NODE_TYPE_LABELS[node.type]})_${suffix}`)
    for (const item of node.evidence ?? []) {
      lines.push(`${shift}  > ${item.quote.replace(/\r?\n/g, "\n" + shift + "  > ")} (${item.source_id})`)
    }
    node.children.forEach((child) => walk(child, depth + 1))
  }
  buildTree(nodes).forEach((root) => walk(root, 0))
  return `${lines.join("\n")}\n`
}

/** Дерево в JSON: та же иерархия, но с id, ЗУН и количеством обоснований. */
export function treeToJson(nodes: ActivityNode[]): string {
  const walk = (node: TreeNode): unknown => ({
    id: node.id,
    type: node.type,
    title: node.title,
    evidence: (node.evidence ?? []).length,
    description: node.description,
    evidence_details: node.evidence ?? [],
    zun: node.zun ?? null,
    children: node.children.map(walk),
  })
  return `${JSON.stringify(buildTree(nodes).map(walk), null, 2)}\n`
}

/** Скачивает текст как файл (клиентский экспорт без участия бэкенда). */
export function downloadText(filename: string, text: string, type = "text/plain;charset=utf-8") {
  const blob = new Blob([text], { type })
  const url = URL.createObjectURL(blob)
  const link = document.createElement("a")
  link.href = url
  link.download = filename
  link.click()
  URL.revokeObjectURL(url)
}
