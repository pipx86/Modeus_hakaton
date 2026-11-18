/**
 * @file tree-flags.ts
 * @description Красные флаги качества дерева: общие названия, дубли, пустые узлы и слабые источники.
 */

import type { ActivityNode, Source } from "@/lib/api/client"
import { provenanceFor } from "@/lib/provenance"

export type TreeFlagCode =
  | "generic_title"
  | "duplicate_title"
  | "no_children"
  | "no_evidence"
  | "weak_source"

export interface TreeFlag {
  code: TreeFlagCode
  label: string
  reason: string
}

const GENERIC_TITLES = new Set([
  "роль",
  "функция",
  "задача",
  "действие",
  "прочее",
  "разное",
  "другое",
  "общее",
])

function normalize(value: string): string {
  return value.trim().toLowerCase().replace(/\s+/g, " ")
}

/**
 * Возвращает флаги по каждому узлу. Правила детерминированные и не ходят в сеть:
 * 1. общее/слишком короткое название;
 * 2. дубль названия среди узлов дерева;
 * 3. промежуточный узел без детей;
 * 4. узел без обоснований;
 * 5. единственная цитата из слабого источника (только документ).
 */
export function computeTreeFlags(
  nodes: ActivityNode[],
  sources?: Map<string, Source>,
): Map<string, TreeFlag[]> {
  const titleCounts = new Map<string, number>()
  for (const node of nodes) {
    const key = normalize(node.title)
    titleCounts.set(key, (titleCounts.get(key) ?? 0) + 1)
  }

  const flags = new Map<string, TreeFlag[]>()
  for (const node of nodes) {
    const reasons: TreeFlag[] = []
    const normalized = normalize(node.title)
    if (GENERIC_TITLES.has(normalized) || normalized.length < 4) {
      reasons.push({
        code: "generic_title",
        label: "Общее название",
        reason: "Название слишком общее: добавьте конкретику (что именно делает специалист).",
      })
    }
    if ((titleCounts.get(normalized) ?? 0) > 1) {
      reasons.push({
        code: "duplicate_title",
        label: "Дубль",
        reason: "Такое название уже есть у другого узла: объедините или уточните.",
      })
    }
    if (node.type !== "action" && !nodes.some((child) => child.parent_id === node.id)) {
      reasons.push({
        code: "no_children",
        label: "Нет детей",
        reason: "Промежуточный уровень без вложенных узлов: добавьте функции/задачи/действия.",
      })
    }
    if ((node.evidence ?? []).length === 0) {
      reasons.push({
        code: "no_evidence",
        label: "Нет обоснования",
        reason: "Узел не подтверждён ни одной цитатой.",
      })
    } else {
      const provenance = provenanceFor(node.evidence ?? [], sources)
      const onlyDocuments = provenance.types.every((type) => type === "document")
      if (provenance.count === 1 && onlyDocuments) {
        reasons.push({
          code: "weak_source",
          label: "Слабый источник",
          reason: "Единственный источник - документ; желательно подтверждение интервью или стандартом.",
        })
      }
    }
    if (reasons.length > 0) flags.set(node.id, reasons)
  }
  return flags
}
