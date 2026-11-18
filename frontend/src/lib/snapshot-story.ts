/**
 * @file snapshot-story.ts
 * @description Человекочитаемый changelog между двумя снимками дерева.
 */

import type { SnapshotDiff } from "@/lib/api/client"
import { NODE_TYPE_LABELS } from "@/components/activity-tree-model"

const FIELD_LABELS: Record<string, string> = {
  title: "название",
  description: "описание",
  type: "тип",
  parent_id: "родитель",
  evidence: "обоснования",
}

/** Собирает текстовое резюме изменений: его можно скопировать в отчёт или чат. */
export function buildSnapshotStory(diff: SnapshotDiff): string {
  const lines = [
    `Изменения дерева: v${diff.base.version} → v${diff.head.version}`,
    `Итого: +${diff.summary.added} добавлено, -${diff.summary.removed} удалено, ~${diff.summary.changed} изменено`,
  ]
  if (diff.added.length > 0) {
    lines.push("", "Добавлены:")
    for (const node of diff.added) {
      lines.push(`- «${node.title}» (${NODE_TYPE_LABELS[node.type] ?? node.type})`)
    }
  }
  if (diff.removed.length > 0) {
    lines.push("", "Удалены:")
    for (const node of diff.removed) {
      lines.push(`- «${node.title}» (${NODE_TYPE_LABELS[node.type] ?? node.type})`)
    }
  }
  if (diff.changed.length > 0) {
    lines.push("", "Изменены:")
    for (const change of diff.changed) {
      const fields = change.fields.map((field) => FIELD_LABELS[field] ?? field).join(", ")
      const before = change.before.title
      const after = change.after.title
      lines.push(
        before === after
          ? `- «${after}»: ${fields}`
          : `- «${before}» → «${after}»: ${fields}`,
      )
    }
  }
  if (diff.summary.added + diff.summary.removed + diff.summary.changed === 0) {
    lines.push("", "Снимки совпадают, различий нет.")
  }
  return `${lines.join("\n")}\n`
}
