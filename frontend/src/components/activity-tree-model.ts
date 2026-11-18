/**
 * @file activity-tree-model.ts
 * @description Модель дерева деятельности: сборка иерархии из плоского списка узлов и подписи уровней.
 */

import type { ActivityNode, NodeType } from "@/lib/api/client"

/** Узел дерева с восстановленными детьми из плоского ответа API. */
export type TreeNode = ActivityNode & { children: TreeNode[] }

/** Подписи типов узлов для дерева и формы редактирования. */
export const NODE_TYPE_LABELS: Record<NodeType, string> = {
  role: "Роль",
  function: "Функция",
  task: "Задача",
  action: "Действие",
}

/** Восстанавливает иерархию дерева из плоского списка узлов за два прохода. */
export function buildTree(nodes: ActivityNode[]): TreeNode[] {
  const map = new Map<string, TreeNode>()
  for (const node of nodes) map.set(node.id, { ...node, children: [] })

  const roots: TreeNode[] = []
  for (const node of nodes) {
    const current = map.get(node.id)
    if (!current) continue
    const parent = node.parent_id ? map.get(node.parent_id) : undefined
    if (parent) parent.children.push(current)
    else roots.push(current)
  }
  return roots
}
