/**
 * @file text.ts
 * @description Приводит типографику ответов API и интерфейса к короткому дефису.
 */

const LONG_DASHES = /[\u2014\u2013]/g

/** Заменяет длинное и среднее тире на обычный дефис. */
export function normalizeText(value: string): string {
  return value.replace(LONG_DASHES, "-")
}

/** Рекурсивно нормализует строки в JSON-подобном значении. */
export function normalizeDashes<T>(value: T): T {
  if (typeof value === "string") return normalizeText(value) as T
  if (Array.isArray(value)) return value.map((item) => normalizeDashes(item)) as T
  if (value && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value).map(([key, item]) => [key, normalizeDashes(item)]),
    ) as T
  }
  return value
}
