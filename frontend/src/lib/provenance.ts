/**
 * @file provenance.ts
 * @description Провенанс узла: какие типы источников подтверждают узел и как объяснить его появление.
 */

import type { Evidence, Source, SourceType } from "@/lib/api/client"
import { SOURCE_TYPE_LABEL } from "@/lib/status"

/** Приоритет источников из domain.SourcePriority: интервью важнее стандарта и вакансии. */
export const SOURCE_PRIORITY: Record<SourceType, number> = {
  interview: 300,
  standard: 200,
  vacancy: 100,
  document: 50,
}

export interface ProvenanceInfo {
  types: SourceType[]
  labels: string[]
  count: number
  unknown: number
}

/** Индекс источников по id: цитаты хранят только source_id. */
export function buildSourceIndex(sources: Source[]): Map<string, Source> {
  return new Map(sources.map((source) => [source.id, source]))
}

/** Уникальные типы источников, подтверждающих узел. */
export function provenanceFor(
  evidence: Evidence[],
  sources: Map<string, Source> | undefined,
): ProvenanceInfo {
  const types = new Set<SourceType>()
  let unknown = 0
  for (const item of evidence) {
    const source = sources?.get(item.source_id)
    if (source) types.add(source.type)
    else unknown += 1
  }
  const sorted = (Object.keys(SOURCE_PRIORITY) as SourceType[]).filter((type) => types.has(type))
  return {
    types: sorted,
    labels: sorted.map((type) => SOURCE_TYPE_LABEL[type] ?? type),
    count: sorted.length,
    unknown,
  }
}

/** Короткая подпись для бейджа в дереве; null, если обоснований нет. */
export function provenanceLabel(info: ProvenanceInfo): string | null {
  if (info.count === 0 && info.unknown === 0) return null
  if (info.count === 0) return "источник"
  return `${info.count} ${info.count === 1 ? "тип" : "типа"}`
}

/** Шаблонное объяснение «почему этот узел здесь» без обращения к LLM. */
export function explainNode(
  title: string,
  evidence: Evidence[],
  sources: Map<string, Source> | undefined,
): string {
  const info = provenanceFor(evidence, sources)
  if (evidence.length === 0) {
    return `Узел «${title}» создан агентом, но обоснований нет: проверьте формулировку вручную.`
  }
  if (info.count === 0) {
    return `Узел «${title}» подтверждён ${evidence.length} цитатами; метаданные источников недоступны.`
  }
  const topType = info.types[0]
  const priority = SOURCE_PRIORITY[topType]
  const unknown = info.unknown > 0 ? `, без метаданных: ${info.unknown}` : ""
  return `Узел «${title}» подтверждён ${evidence.length} цитатами из ${info.count} типов источников: ${info.labels.join(", ")}${unknown}. Приоритет у «${SOURCE_TYPE_LABEL[topType]}» (${priority}) - по нему синтезатор выбирал формулировку.`
}
