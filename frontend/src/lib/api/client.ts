/**
 * @file client.ts
 * @description Настроенный openapi-fetch клиент: базовый URL, JWT и общие типы API.
 */

import createClient from "openapi-fetch"
import { frontendLog } from "@/lib/frontend-log"
import { normalizeDashes } from "@/lib/text"
import { getValidToken } from "@/lib/auth"
import type { paths, components } from "./schema"

// В dev запросы идут на тот же origin через Vite-прокси (он добавляет Authorization).
// В тестах jsdom требует абсолютный URL, поэтому там подставляется localhost.
const configuredBase = import.meta.env.VITE_API_URL
export const apiBaseUrl =
  configuredBase
    ? `${configuredBase}/api/v1`
    : import.meta.env.MODE === "test"
      ? "http://localhost:8080/api/v1"
      : "/api/v1"

export const apiToken = import.meta.env.VITE_API_TOKEN ?? ""

/** Убирает длинные тире из JSON-ответа, не трогая бинарные и текстовые форматы. */
async function normalizeJSONResponse(response: Response): Promise<Response> {
  const contentType = response.headers.get("content-type") ?? ""
  if (
    !contentType.includes("application/json") ||
    response.status === 204 ||
    typeof response.clone !== "function"
  ) {
    return response
  }
  try {
    const data: unknown = await response.clone().json()
    const headers = new Headers(response.headers)
    headers.delete("content-length")
    headers.delete("content-encoding")
    return new Response(JSON.stringify(normalizeDashes(data)), {
      status: response.status,
      statusText: response.statusText,
      headers,
    })
  } catch {
    return response
  }
}

const normalizeFetch: typeof fetch = async (input, init) => {
  const response = await fetch(input, init)
  return normalizeJSONResponse(response)
}

export const apiClient = createClient<paths>({ baseUrl: apiBaseUrl, fetch: normalizeFetch })

// Токен нужен, если фронтенд обращается к API напрямую (без прокси).
apiClient.use({
  onRequest({ request }) {
    const token = getValidToken() ?? apiToken
    if (token) request.headers.set("Authorization", `Bearer ${token}`)
    return request
  },
  onResponse({ request, response }) {
    if (response.status >= 500) {
      frontendLog.error("API 5xx", {
        method: request.method,
        url: request.url,
        status: response.status,
      })
    } else if (response.status >= 400) {
      frontendLog.warn("API 4xx", {
        method: request.method,
        url: request.url,
        status: response.status,
      })
    }
  },
  onError({ request, error }) {
    frontendLog.error("API request failed", {
      method: request.method,
      url: request.url,
      error: String(error),
    })
  },
})

export type HealthResponse = components["schemas"]["HealthResponse"]
export type ApiError = components["schemas"]["Error"]
export type NodeType = components["schemas"]["NodeType"]
export type SourceType = components["schemas"]["SourceType"]
export type RunStatus = components["schemas"]["RunStatus"]
export type Evidence = components["schemas"]["Evidence"]
export type ActivityNode = components["schemas"]["Node"]
export type Source = components["schemas"]["Source"]
export type Run = components["schemas"]["Run"]
export type ChatMessage = components["schemas"]["ChatMessage"]
export type Conflict = components["schemas"]["Conflict"]
export type RunEvent = components["schemas"]["RunEvent"]
export type Insights = components["schemas"]["Insights"]
export type CoverageResponse = components["schemas"]["CoverageResponse"]
export type RunCompare = components["schemas"]["RunCompare"]
export type RunEstimate = components["schemas"]["RunEstimate"]
export type RunCost = components["schemas"]["RunCost"]
export type CorrelationResponse = components["schemas"]["CorrelationResponse"]
export type ReviewProgress = components["schemas"]["ReviewProgress"]
export type ZunSuggestion = components["schemas"]["ZunSuggestion"]
export type EvidenceContext = components["schemas"]["EvidenceContext"]

// ---------------------------------------------------------------------------
// Эндпоинты, которых ещё нет в сгенерированной OpenAPI-схеме: снимки дерева,
// сравнение снимков и очередь спорных подтверждений. Схему намеренно не
// регенерировали, поэтому ниже - небольшие raw-fetch обёртки на том же
// baseUrl и с той же Bearer-авторизацией, что и у openapi-клиента. Ошибки
// повторяют контракт `{code, message}` и сохраняют HTTP-статус: экран сам
// решает, показать «нет прав» (403) или «нужен PostgreSQL» (501).
// ---------------------------------------------------------------------------

/** Снимок дерева исследования (`GET /runs/{id}/snapshots`). */export interface RunSnapshot {
  id: string
  run_id: string
  version: number
  /** Сохранённое дерево снимка; сравнение разбирает его на бэкенде. */
  tree?: unknown
  created_at?: string
}

/** Ссылка на версию снимка в ответе сравнения. */
export interface SnapshotVersionRef {
  id: string
  version: number
}

/** Изменённый между снимками узел: какие поля поменялись и каким было значение. */
export interface SnapshotNodeChange {
  id: string
  before: ActivityNode
  after: ActivityNode
  /** Изменённые поля в порядке контракта: title, description, type, parent_id, evidence. */
  fields: string[]
}

export interface SnapshotDiffSummary {
  added: number
  removed: number
  changed: number
}

/** Ответ `GET /runs/{id}/snapshots/{snapshot_id}/diff?against={base_snapshot_id}`. */
export interface SnapshotDiff {
  run_id: string
  base: SnapshotVersionRef
  head: SnapshotVersionRef
  added: ActivityNode[]
  removed: ActivityNode[]
  changed: SnapshotNodeChange[]
  summary: SnapshotDiffSummary
  /** Человекочитаемый changelog от бэкенда (если доступен). */
  story?: string
  story_lines?: string[]
}

/** Статусы очереди проверки (`GET /reviews/evidence`). */
export type ReviewEvidenceStatus = "pending" | "rejected"

/** Строка очереди спорных подтверждений с контекстом для решения эксперта. */
export interface ReviewQueueItem {
  id: string
  run_id: string
  node_id: string
  node_title?: string
  source_id: string
  source_title?: string
  source_url?: string
  ref_id?: string
  quote: string
  status: ReviewEvidenceStatus
}

/**
 * Ошибка raw-запроса к API. В отличие от `{data,error}` openapi-клиента здесь
 * выбрасывается исключение: хендлеры ловят его и точечно обрабатывают
 * HTTP-статус (403 - нет прав, 501 - бэкенд без PostgreSQL, 0 - нет сети).
 */
export class ApiRequestError extends Error {
  readonly status: number
  readonly code?: string

  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = "ApiRequestError"
    this.status = status
    this.code = code
  }
}

/**
 * Выполняет запрос к API с токеном текущей сессии.
 *
 * @param path Путь от `apiBaseUrl` (начинается со слэша), с уже собранным query.
 * @param init Дополнительные опции fetch (метод, тело).
 */
async function requestApi<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set("Accept", "application/json")
  const token = getValidToken() ?? apiToken
  if (token) headers.set("Authorization", `Bearer ${token}`)

  let response: Response
  try {
    response = await fetch(`${apiBaseUrl}${path}`, { ...init, headers })
  } catch (error) {
    frontendLog.error("API request failed", { path, error: String(error) })
    throw new ApiRequestError("Сервер недоступен: проверьте соединение.", 0)
  }

  if (!response.ok) {
    let code: string | undefined
    let message = `Запрос завершился с HTTP ${response.status}`
    try {
      const body = (await response.json()) as { code?: unknown; message?: unknown }
      if (typeof body?.code === "string") code = body.code
      if (typeof body?.message === "string" && body.message.trim()) message = body.message
    } catch {
      // Тело не JSON (например, HTML страницы ошибки прокси) - оставляем сообщение по статусу.
    }
    if (response.status >= 500) frontendLog.error("API 5xx", { path, status: response.status })
    else frontendLog.warn("API 4xx", { path, status: response.status })
    throw new ApiRequestError(message, response.status, code)
  }

  if (response.status === 204) return undefined as T
  try {
    return (await response.json()) as T
  } catch {
    throw new ApiRequestError("Сервер вернул некорректный ответ.", response.status)
  }
}

/** Список снимков исследования, новые версии приходят последними. */
export async function listRunSnapshots(runId: string): Promise<RunSnapshot[]> {
  return requestApi<RunSnapshot[]>(`/runs/${encodeURIComponent(runId)}/snapshots`)
}

export interface LearningPlanStage {
  title: string
  goal: string
  topics: string[]
  practice: string[]
}

export interface LearningPlan {
  profession: string
  summary: string
  weeks: number
  stages: LearningPlanStage[]
  sources?: string[]
}

/** Возвращает сохранённый план обучения исследования. */
export async function getLearningPlan(runId: string): Promise<LearningPlan> {
  return requestApi<LearningPlan>(`/runs/${encodeURIComponent(runId)}/learning-plan`)
}

export interface LearningPlanStage {
  title: string
  goal: string
  topics: string[]
  practice: string[]
}

export interface LearningPlan {
  profession: string
  summary: string
  weeks: number
  stages: LearningPlanStage[]
  sources?: string[]
}

/** Составляет план обучения для новичка по выбранной профессии. */
export async function createLearningPlan(profession: string): Promise<LearningPlan> {
  return requestApi<LearningPlan>("/learning-plans", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ profession }),
  })
}

export interface GitHubLanguage {
  name: string
  repositories: number
}

export interface GitHubProject {
  name: string
  url: string
  description?: string
  language?: string
  topics?: string[]
  stars: number
  forks: number
  updated_at?: string
}

export interface GitHubAssessment {
  username: string
  name?: string
  profile_url: string
  avatar_url?: string
  profession?: string
  score: number
  profession_match: number
  repositories: number
  original_repositories: number
  total_stars: number
  total_forks: number
  languages: GitHubLanguage[]
  skills: string[]
  projects: GitHubProject[]
  summary: string
  warnings?: string[]
  fetched_at: string
  profession_matched: boolean
}

/** Читает публичные репозитории GitHub и оценивает профиль специалиста. */
export async function evaluateGitHub(username: string, profession: string): Promise<GitHubAssessment> {
  return requestApi<GitHubAssessment>("/github/evaluate", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, profession }),
  })
}

/**
 * Сравнивает `head` (новый снимок, id в пути) с `base` (параметр `against`).
 * Порядок аргументов совпадает с контрактом бэкенда: head, затем base.
 */
export async function compareRunSnapshots(
  runId: string,
  headSnapshotId: string,
  baseSnapshotId: string,
): Promise<SnapshotDiff> {
  const query = new URLSearchParams({ against: baseSnapshotId }).toString()
  return requestApi<SnapshotDiff>(
    `/runs/${encodeURIComponent(runId)}/snapshots/${encodeURIComponent(headSnapshotId)}/diff?${query}`,
  )
}

/**
 * Очередь спорных подтверждений. Доступна только роли admin;
 * остальным сервер отвечает 403, и экран показывает это сообщением.
 */
export async function listReviewEvidence(
  status: ReviewEvidenceStatus,
  limit = 50,
  offset = 0,
): Promise<ReviewQueueItem[]> {
  const query = new URLSearchParams({
    status,
    limit: String(limit),
    offset: String(offset),
  }).toString()
  return requestApi<ReviewQueueItem[]>(`/reviews/evidence?${query}`)
}
