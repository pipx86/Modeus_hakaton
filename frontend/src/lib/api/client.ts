/**
 * @file client.ts
 * @description Настроенный openapi-fetch клиент: базовый URL, JWT и общие типы API.
 */

import createClient from "openapi-fetch"
import { frontendLog } from "@/lib/frontend-log"
import { stripControlChars } from "@/lib/security"
import { normalizeDashes, normalizeText } from "@/lib/text"
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
export type EvidenceStatus = components["schemas"]["EvidenceStatus"]
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
  /**
   * Новые и удалённые обоснования узла. Бэкенд добавляет поля аддитивно: старые
   * ответы их не содержат, поэтому UI обязан работать и без них.
   */
  evidence_added?: SnapshotEvidenceRef[]
  evidence_removed?: SnapshotEvidenceRef[]
}

/** Обоснование в дельте снимков: цитата и материал, на который она ссылается. */
export interface SnapshotEvidenceRef {
  id: string
  quote: string
  source_id: string
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
  /**
   * Необязательные общие дельты обоснований: бэкенд может отдать их как на уровне
   * изменённого узла, так и на уровне всего сравнения. UI поддерживает обе формы.
   */
  evidence_added?: SnapshotEvidenceRef[]
  evidence_removed?: SnapshotEvidenceRef[]
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

export async function generateProgramLinks(runId:string):Promise<ProgramLink[]> {
  return requestApi(`/runs/${encodeURIComponent(runId)}/program-links/generate`,{method:"POST"})
}

/**
 * Ошибка raw-запроса к API. В отличие от `{data,error}` openapi-клиента здесь
 * выбрасывается исключение: хендлеры ловят его и точечно обрабатывают
 * HTTP-статус (403 - нет прав, 501 - бэкенд без PostgreSQL, 0 - нет сети).
 */
export class ApiRequestError extends Error {
  readonly status: number
  readonly code?: string
  /** Предупреждения разбора из тела ошибки (например, 422 при импорте программы). */
  readonly warnings: string[]

  constructor(message: string, status: number, code?: string, warnings: string[] = []) {
    super(message)
    this.name = "ApiRequestError"
    this.status = status
    this.code = code
    this.warnings = warnings
  }
}

/** Тело ошибки API; `warnings` бэкенд может прислать рядом с `message` или в `report`. */
interface ApiErrorBody {
  code?: unknown
  message?: unknown
  warnings?: unknown
  report?: { warnings?: unknown } | null
}

/** Достаёт строковые предупреждения из тела ошибки, отбрасывая пустые значения. */
function parseErrorWarnings(body: ApiErrorBody | null): string[] {
  const raw = Array.isArray(body?.warnings)
    ? body.warnings
    : Array.isArray(body?.report?.warnings)
      ? body.report.warnings
      : []
  return raw
    .filter((item): item is string => typeof item === "string" && item.trim() !== "")
    .map(normalizeText)
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
    let warnings: string[] = []
    let message = `Запрос завершился с HTTP ${response.status}`
    try {
      const body = (await response.json()) as ApiErrorBody
      if (typeof body?.code === "string") code = body.code
      if (typeof body?.message === "string" && body.message.trim()) message = normalizeText(body.message)
      warnings = parseErrorWarnings(body)
    } catch {
      // Тело не JSON (например, HTML страницы ошибки прокси) - оставляем сообщение по статусу.
    }
    if (response.status >= 500) frontendLog.error("API 5xx", { path, status: response.status })
    else frontendLog.warn("API 4xx", { path, status: response.status })
    throw new ApiRequestError(message, response.status, code, warnings)
  }

  if (response.status === 204) return undefined as T
  try {
    return normalizeDashes((await response.json()) as T) as T
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

export interface InterviewProgress {
  asked: number
  total: number
  remaining: number
  average_quality: number
  last_quality: number
  covered_topics: string[]
  uncovered_topics: string[]
  contradictions?: string[]
}

/** Возвращает живой прогресс активного интервью. */
export async function getInterviewProgress(runId: string): Promise<InterviewProgress> {
  return requestApi<InterviewProgress>(`/runs/${encodeURIComponent(runId)}/interview-progress`)
}

export interface InterviewABVariant {
  prompt_version: string
  sessions: number
  completed: number
  questions: number
  answers: number
  average_quality: number
  completion_rate: number
}

/** Сравнение основной и B-версии промпта интервьюера. */
export async function getInterviewABStats(): Promise<{ variants: InterviewABVariant[] }> {
  return requestApi<{ variants: InterviewABVariant[] }>("/interview/ab-stats")
}

export interface SkillTrend {
  skill: string
  current_frequency?: number
  predicted_change_pct?: number
  confidence?: number
  trend_direction?: string
  method?: string
}

export interface SkillTrendsResponse {
  series: string
  direction: string
  months: number
  total: number
  forecasts: SkillTrend[]
}

export async function getSkillTrends(direction: "growing" | "declining", limit = 15): Promise<SkillTrendsResponse> {
  return requestApi<SkillTrendsResponse>(`/forecast/top?direction=${direction}&n=${limit}`)
}

export interface RegressionCoefficient {
  name: string
  estimate: number
  std_error: number
  t_stat: number
}

export interface RegressionSkill {
  skill: string
  samples: number
  r2: number
  intercept: number
  coefficients: RegressionCoefficient[]
}

export interface RegressionResponse {
  target: string
  description: string
  samples: number
  skills: RegressionSkill[]
}

export async function getSkillRegression(skill?: string): Promise<RegressionResponse> {
  const query = skill ? `?skill=${encodeURIComponent(skill)}` : ""
  return requestApi<RegressionResponse>(`/forecast/regression${query}`)
}

export interface TreeForecastNode {
  id: string
  title: string
  type: string
  status: "grow" | "decline" | "stable"
  confidence: number
  reason: string
  trend_pct?: number
}

export interface TreeForecastNewNode {
  title: string
  type: string
  reason: string
  confidence: number
}

export interface TreeForecastResponse {
  profession: string
  run_id?: string
  horizon_months: number
  scenario: boolean
  generated_at: string
  nodes: TreeForecastNode[]
  new_nodes: TreeForecastNewNode[]
  summary: string
}

export async function getTreeForecast(profession: string): Promise<TreeForecastResponse> {
  return requestApi<TreeForecastResponse>(`/forecast/tree?profession=${encodeURIComponent(profession)}`)
}

export interface ProfileImport {
  source: string
  filename?: string
  url?: string
  title?: string
  text: string
}

/** Извлекает текст резюме PDF/DOCX для персонализации интервью. */
export async function importResume(file: File): Promise<ProfileImport> {
  const body = new FormData()
  body.append("file", file)
  return requestApi<ProfileImport>("/profile/resume", { method: "POST", body })
}

/** Загружает публичный профиль HH по ссылке на резюме. */
export async function importHHProfile(url: string): Promise<ProfileImport> {
  return requestApi<ProfileImport>("/profile/hh", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ url }),
  })
}

/** Прикрепляет файл-доказательство к текущему ответу интервью. */
export async function attachInterviewEvidence(runId: string, file: File): Promise<void> {
  const body = new FormData()
  body.append("file", file)
  await requestApi(`/runs/${encodeURIComponent(runId)}/attachments`, { method: "POST", body })
}

/** Прикрепляет ссылку-доказательство к текущему ответу интервью. */
export async function attachInterviewLink(runId: string, url: string, title: string): Promise<void> {
  await requestApi(`/runs/${encodeURIComponent(runId)}/attachments/link`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ url, title }),
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

// ---------------------------------------------------------------------------
// MVP-план: проверка узлов дерева, сопоставления с образовательной программой,
// сводка проверки и история интервью. Этих эндпоинтов ещё нет в сгенерированной
// OpenAPI-схеме, поэтому ниже - ручные типы и те же raw-fetch обёртки, что и выше.
// ---------------------------------------------------------------------------

/** Статус проверки узла методистом. */
export type NodeReviewStatus = "pending" | "approved" | "rejected"

/** Решение по узлу, которое отправляется в `POST /nodes/{id}/review`. */
export type NodeReviewDecision = "approved" | "rejected"

/**
 * Поля проверки, которые бэкенд добавляет к узлу дерева. Все они необязательные:
 * старый бэкенд и legacy-моки их не отдают, а UI считает такой узел непроверенным.
 */
export interface NodeReviewFields {
  review_status?: NodeReviewStatus | null
  review_comment?: string | null
  reviewed_at?: string | null
}

/** Узел дерева вместе с полями проверки (`GET /runs/{id}/tree`). */
export type ReviewableNode = ActivityNode & NodeReviewFields

/**
 * Сохраняет решение методиста по узлу.
 *
 * @param nodeId Id узла дерева.
 * @param status `approved` или `rejected`.
 * @param comment Необязательный комментарий; пустая строка допустима.
 */
export async function setNodeReview(
  nodeId: string,
  status: NodeReviewDecision,
  comment = "",
): Promise<ReviewableNode> {
  return requestApi<ReviewableNode>(`/nodes/${encodeURIComponent(nodeId)}/review`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ status, comment }),
  })
}

/** Статус связи узла деятельности с элементом образовательной программы. */
export type ProgramLinkStatus = "proposed" | "approved" | "rejected"

/** Связь узла дерева с элементом программы (`GET /runs/{id}/program-links`). */
export interface ProgramLink {
  id: string
  run_id: string
  snapshot_id?: string
  snapshot_version?: number
  node_id: string
  node_title: string
  node_type: NodeType
  program_id: string
  program_title: string
  competency_id: string
  competency_title: string
  ksa_id: string | null
  ksa_title: string | null
  status: ProgramLinkStatus
  /** Объяснение, почему система предложила именно эту связь. */
  reason: string
  source: "system" | "methodist"
  reviewed_at: string | null
  evidence?: Evidence[]
}

export interface ProgramLinkTarget { competency_id:string;competency_title:string;ksa_id:string|null;ksa_title:string|null }
export async function getProgramLinkTargets(programId:string):Promise<ProgramLinkTarget[]> {
  const program=await requestApi<{
    competencies?:Array<{id:string;title:string;ksa?:Array<{id:string;title:string}>}>
  }>(`/academic/programs/${encodeURIComponent(programId)}`)
  return (program.competencies??[]).flatMap((competency)=>[
    {competency_id:competency.id,competency_title:competency.title,ksa_id:null,ksa_title:null},
    ...(competency.ksa??[]).map((ksa)=>({competency_id:competency.id,competency_title:competency.title,ksa_id:ksa.id,ksa_title:ksa.title})),
  ])
}

/**
 * Список связей исследования с программой. Если передан `nodeId`, он уходит
 * query-параметром `node_id`: бэкенд может поддержать фильтр, а компонент всё
 * равно отфильтрует ответ на клиенте, чтобы не показывать чужие узлы.
 */
export async function listProgramLinks(runId: string, nodeId?: string): Promise<ProgramLink[]> {
  const query = nodeId ? `?node_id=${encodeURIComponent(nodeId)}` : ""
  return requestApi<ProgramLink[]>(`/runs/${encodeURIComponent(runId)}/program-links${query}`)
}

/** Подтверждает или отклоняет связь с элементом программы. */
export async function reviewProgramLink(
  runId: string,
  linkId: string,
  status: NodeReviewDecision,
  reason = "",
  target?: {competency_id:string;ksa_id:string|null},
): Promise<ProgramLink> {
  return requestApi<ProgramLink>(
    `/runs/${encodeURIComponent(runId)}/program-links/${encodeURIComponent(linkId)}/review`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ status, reason, ...target }),
    },
  )
}

/** Счётчики проверки по одной сущности сводки. */
export interface ReviewSummarySection {
  pending: number
  approved: number
  rejected: number
  total: number
}

/** Ответ `GET /runs/{id}/review/summary`. */
export interface ReviewSummary {
  nodes: ReviewSummarySection
  evidence: ReviewSummarySection
  links: ReviewSummarySection
}

/** Сводка проверки исследования: узлы, обоснования и сопоставления. */
export async function getReviewSummary(runId: string): Promise<ReviewSummary> {
  return requestApi<ReviewSummary>(`/runs/${encodeURIComponent(runId)}/review/summary`)
}

/** Статус интервью в истории исследования. */
export type InterviewStatus = "active" | "completed" | "failed"

/** Одно интервью исследования (`GET /runs/{id}/interviews`). */
export interface RunInterview {
  id: string
  run_id: string
  respondent_name?: string
  respondent_role?: string
  status: InterviewStatus
  started_at: string
  finished_at?: string
}

/** Цитата-ссылка внутри сообщения интервью; форма не гарантирована контрактом. */
export interface InterviewCitation {
  id?: string
  source_id?: string
  ref_id?: string
  quote?: string
  title?: string
}

/** Сообщение истории интервью (`GET /runs/{id}/interviews/{interview_id}/messages`). */
export interface InterviewMessage {
  id: string
  role: "user" | "agent"
  content: string
  created_at: string
  citations?: InterviewCitation[]
}

/** Список интервью исследования. */
export async function listRunInterviews(runId: string): Promise<RunInterview[]> {
  return requestApi<RunInterview[]>(`/runs/${encodeURIComponent(runId)}/interviews`)
}

export async function startRunInterview(runId: string, respondentName: string, respondentRole: string): Promise<RunInterview> {
  return requestApi<RunInterview>(`/runs/${encodeURIComponent(runId)}/interviews`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ respondent_name: respondentName, respondent_role: respondentRole }),
  })
}

export async function setReviewAccess(runId: string, email: string, grant: boolean): Promise<void> {
  await requestApi(`/runs/${encodeURIComponent(runId)}/review-access`, {
    method: grant ? "POST" : "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email }),
  })
}

/** История сообщений выбранного интервью. */
export async function listInterviewMessages(
  runId: string,
  interviewId: string,
): Promise<InterviewMessage[]> {
  return requestApi<InterviewMessage[]>(
    `/runs/${encodeURIComponent(runId)}/interviews/${encodeURIComponent(interviewId)}/messages`,
  )
}

/** Ожидаемый результат исследования, который выбирают при запуске. */
export type RunResultKind = "activity" | "program"

/**
 * Программа в ответе `GET /academic/programs`. Поля необязательные: бэкенд может
 * отдать `title` или `name`, `code` часто отсутствует.
 */
export interface AcademicProgram {
  id?: string
  title?: string
  name?: string
  code?: string
}

/** Нормализованный вариант программы для выпадающего списка. */
export interface AcademicProgramOption {
  id: string
  title: string
  code?: string
}

/** Загружает образовательные программы для режима «сопоставление с программой». */
export async function listAcademicPrograms(): Promise<AcademicProgramOption[]> {
  const data = await requestApi<AcademicProgram[]>("/academic/programs")
  if (!Array.isArray(data)) return []
  const options: AcademicProgramOption[] = []
  const seen = new Set<string>()
  for (const raw of data) {
    if (!raw || typeof raw !== "object") continue
    const id = typeof raw.id === "string" ? raw.id.trim() : ""
    if (!id || seen.has(id)) continue
    const title = (typeof raw.title === "string" && raw.title.trim()) ||
      (typeof raw.name === "string" && raw.name.trim()) ||
      id
    const code = typeof raw.code === "string" && raw.code.trim() ? raw.code.trim() : undefined
    seen.add(id)
    options.push({ id, title, code })
  }
  return options
}

/** Тело запуска исследования: базовые поля плюс выбор результата MVP. */
export interface CreateRunInput {
  profession: string
  type?: "chat" | "batch" | "learning"
  transcript?: string
  github_username?: string
  github_consent?: boolean
  github_exclude?: string[]
  profile_context?: string
  result_kind?: RunResultKind
  program_id?: string
}

/** Запускает исследование (`POST /runs`). */
export async function createRun(input: CreateRunInput): Promise<Run> {
  return requestApi<Run>("/runs", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  })
}

/** Ответ `POST /runs/{id}/retry`: повторный анализ поставлен в очередь. */
export interface RetryRunResponse {
  run_id?: string
  status?: RunStatus
  retried?: boolean
  message?: string
}

/**
 * Повторяет анализ упавшего интервью (`POST /runs/{id}/retry`).
 *
 * Backend отвечает 202, а конфликтные случаи - 409 с кодом:
 * `TREE_ALREADY_EXISTS` (дерево уже построено) или `RETRY_IN_PROGRESS`
 * (анализ уже идёт). Они приходят как {@link ApiRequestError}, чтобы экран
 * показал точное объяснение вместо общего текста.
 */
export async function retryRun(runId: string): Promise<RetryRunResponse> {
  return requestApi<RetryRunResponse>(`/runs/${encodeURIComponent(runId)}/retry`, {
    method: "POST",
  })
}

// ---------------------------------------------------------------------------
// P1-карточки MVP: состав использованных материалов, основания из интервью и
// отчёт о покрытии программы деятельностью. Эндпоинтов пока нет в сгенерированной
// OpenAPI-схеме, формы фиксированы планом MVP, поэтому здесь ручные типы и те же
// raw-fetch обёртки, что у снимков и проверки узлов.
// ---------------------------------------------------------------------------

/**
 * Материал, привязанный к исследованию (`GET /runs/{id}/sources`).
 * `evidence_count` показывает, сколько обоснований ссылается на материал.
 */
export interface RunSource {
  id: string
  type: SourceType
  title: string
  source_url?: string | null
  created_at?: string | null
  evidence_count?: number | null
}

/** Состав использованных материалов исследования. */
export async function listRunSources(runId: string): Promise<RunSource[]> {
  const data = await requestApi<RunSource[]>(`/runs/${encodeURIComponent(runId)}/sources`)
  // Старый бэкенд мог отвечать объектом вместо массива: экран покажет пустое состояние.
  return Array.isArray(data) ? data : []
}

export interface SourceChange {
  source_id: string
  source_title: string
  node_id: string
  node_title: string
  evidence_id: string
  changed_at: string
}

export async function listSourceChanges(runId: string): Promise<SourceChange[]> {
  const result = await requestApi<SourceChange[]>(`/runs/${encodeURIComponent(runId)}/source-changes`)
  return Array.isArray(result) ? result : []
}

/**
 * Обоснование, которое интервью дало дереву деятельности
 * (`GET /runs/{id}/interviews/{interview_id}/evidence`).
 */
export interface InterviewEvidence {
  evidence_id: string
  node_id: string
  node_title: string
  node_type: NodeType
  quote: string
  source_id: string
  source_title?: string | null
  status: EvidenceStatus
}

/** Основания, связанные с ответами выбранного интервью. */
export async function listInterviewEvidence(
  runId: string,
  interviewId: string,
): Promise<InterviewEvidence[]> {
  const data = await requestApi<InterviewEvidence[]>(
    `/runs/${encodeURIComponent(runId)}/interviews/${encodeURIComponent(interviewId)}/evidence`,
  )
  return Array.isArray(data) ? data : []
}

/** Статус узла в отчёте о покрытии: подтверждён, только предложен или без связи. */
export type ProgramCoverageStatus = "confirmed" | "proposed" | "none"

/** Связь узла деятельности с элементом программы в отчёте о покрытии. */
export interface ProgramCoverageLink {
  link_id: string
  competency_id: string
  competency_title: string
  status: ProgramLinkStatus
  reason: string
}

/** Один узел дерева в отчёте о покрытии программы. */
export interface ProgramCoverageNode {
  node_id: string
  node_title: string
  node_type: NodeType
  status: ProgramCoverageStatus
  links: ProgramCoverageLink[]
}

/** Компетенция программы, связанная с деятельностью, и число её связей. */
export interface ProgramCoverageCompetency {
  competency_id: string
  competency_title: string
  links_count: number
}

/** Счётчики отчёта о покрытии программы. */
export interface ProgramCoverageCounts {
  confirmed: number
  proposed: number
  none: number
  total: number
}

/** Ответ `GET /runs/{id}/program-coverage`. */
export interface ProgramCoverage {
  run_id: string
  program_id: string
  program_title: string
  nodes: ProgramCoverageNode[]
  competencies: ProgramCoverageCompetency[]
  counts: ProgramCoverageCounts
}

/** Отчёт о покрытии программы деятельностью исследования. */
export async function getProgramCoverage(runId: string): Promise<ProgramCoverage> {
  const data = await requestApi<ProgramCoverage>(
    `/runs/${encodeURIComponent(runId)}/program-coverage`,
  )
  return {
    run_id: typeof data?.run_id === "string" ? data.run_id : runId,
    program_id: typeof data?.program_id === "string" ? data.program_id : "",
    program_title: typeof data?.program_title === "string" ? data.program_title : "",
    nodes: Array.isArray(data?.nodes) ? data.nodes : [],
    competencies: Array.isArray(data?.competencies) ? data.competencies : [],
    counts: {
      confirmed: Number(data?.counts?.confirmed ?? 0) || 0,
      proposed: Number(data?.counts?.proposed ?? 0) || 0,
      none: Number(data?.counts?.none ?? 0) || 0,
      total: Number(data?.counts?.total ?? 0) || 0,
    },
  }
}

// ---------------------------------------------------------------------------
// Карточки 31 и 32 плана MVP: отчёт об изменениях деятельности между двумя
// исследованиями и предложения по программе на основе сопоставления.
// Эндпоинтов пока нет в сгенерированной OpenAPI-схеме, формы фиксированы
// планом MVP, поэтому здесь ручные типы и те же raw-fetch обёртки, что у
// покрытия программы и снимков.
// ---------------------------------------------------------------------------

/** Краткая карточка исследования для выбора «противника» в сравнении. */
export interface RunListItem {
  id: string
  profession: string
  status?: RunStatus
  created_at?: string
}

/**
 * Список исследований для выбора второго прогона. Форма ответа `GET /runs`
 * исторически нестабильна, поэтому поля нормализуются, а записи без id
 * отбрасываются.
 */
export async function listRuns(): Promise<RunListItem[]> {
  const data = await requestApi<RunListItem[]>("/runs")
  if (!Array.isArray(data)) return []
  const runs: RunListItem[] = []
  for (const raw of data) {
    if (!raw || typeof raw !== "object") continue
    const id = typeof raw.id === "string" ? raw.id.trim() : ""
    if (!id) continue
    runs.push({
      id,
      profession: typeof raw.profession === "string" ? raw.profession : "",
      status: typeof raw.status === "string" ? (raw.status as RunStatus) : undefined,
      created_at: typeof raw.created_at === "string" ? raw.created_at : undefined,
    })
  }
  return runs
}

/** Материал, который подтверждает узел и не встречается в дереве противника. */
export interface ActivityChangeSource {
  source_id: string
  source_title: string
  source_type: SourceType
}

/** Поля узла, изменение которых показывает отчёт. */
export type ActivityChangeField = "title" | "description" | "type"

/** Узел в одной из категорий отчёта об изменениях деятельности. */
export interface ActivityChangeNode {
  node_id: string
  node_title: string
  node_type: NodeType
  parent_title: string | null
  evidence_count: number
  new_sources: ActivityChangeSource[]
  /** Только у changed: изменившиеся поля в порядке title, description, type. */
  changed_fields?: ActivityChangeField[]
}

/** Счётчики отчёта об изменениях деятельности. */
export interface ActivityChangeSummary {
  added: number
  removed: number
  changed: number
  sources_added: number
  sources_removed: number
}

/** Ответ `GET /runs/{id}/activity-changes?against={run_id}`. */
export interface ActivityChangeReport {
  run_id: string
  against_run_id: string
  same_profession: boolean
  added: ActivityChangeNode[]
  removed: ActivityChangeNode[]
  changed: ActivityChangeNode[]
  summary: ActivityChangeSummary
}

/**
 * Сравнивает деятельность двух исследований. Узлы сопоставляются на бэкенде
 * семантически (тип + нормализованное название + путь предков), поэтому
 * added/removed - это действительно появившиеся и исчезнувшие элементы, а не
 * разница идентификаторов.
 */
export async function getActivityChanges(
  runId: string,
  againstRunId: string,
): Promise<ActivityChangeReport> {
  const query = new URLSearchParams({ against: againstRunId }).toString()
  const data = await requestApi<ActivityChangeReport>(
    `/runs/${encodeURIComponent(runId)}/activity-changes?${query}`,
  )
  return {
    run_id: typeof data?.run_id === "string" ? data.run_id : runId,
    against_run_id:
      typeof data?.against_run_id === "string" ? data.against_run_id : againstRunId,
    same_profession: data?.same_profession === true,
    added: Array.isArray(data?.added) ? data.added : [],
    removed: Array.isArray(data?.removed) ? data.removed : [],
    changed: Array.isArray(data?.changed) ? data.changed : [],
    summary: {
      added: Number(data?.summary?.added ?? 0) || 0,
      removed: Number(data?.summary?.removed ?? 0) || 0,
      changed: Number(data?.summary?.changed ?? 0) || 0,
      sources_added: Number(data?.summary?.sources_added ?? 0) || 0,
      sources_removed: Number(data?.summary?.sources_removed ?? 0) || 0,
    },
  }
}

/** Виды предложений по программе (карточка 32). */
export type ProgramProposalKind =
  | "unmapped_activity"
  | "unconfirmed_competency"
  | "uncovered_competency"
  | "conflicting_links"

/** Приоритет предложения: высокий, средний или низкий. */
export type ProgramProposalPriority = "high" | "medium" | "low"

/** На что указывает предложение: узел деятельности или компетенция программы. */
export interface ProgramSuggestionTarget {
  node_id?: string
  competency_id?: string
  competency_title?: string
}

/** Одно предложение системы; решение остаётся за методистом. */
export interface ProgramProposal {
  id: string
  kind: ProgramProposalKind
  target: ProgramSuggestionTarget
  rationale: string
  priority: ProgramProposalPriority
  status: "proposal"
}

/** Ответ `GET /runs/{id}/program-suggestions`. */
export interface ProgramSuggestions {
  run_id: string
  program_id: string | null
  program_title: string | null
  generated_at: string
  proposals: ProgramProposal[]
  /** Нейтральное пояснение, в том числе для пустого списка предложений. */
  summary?: string
  disclaimer: string
}

/**
 * Предложения по учебным изменениям на основе сопоставления деятельности с
 * программой. Ничего не меняет: система только предлагает.
 */
export async function getProgramSuggestions(runId: string): Promise<ProgramSuggestions> {
  const data = await requestApi<ProgramSuggestions>(
    `/runs/${encodeURIComponent(runId)}/program-suggestions`,
  )
  return {
    run_id: typeof data?.run_id === "string" ? data.run_id : runId,
    program_id: typeof data?.program_id === "string" ? data.program_id : null,
    program_title: typeof data?.program_title === "string" ? data.program_title : null,
    generated_at: typeof data?.generated_at === "string" ? data.generated_at : "",
    proposals: Array.isArray(data?.proposals) ? data.proposals : [],
    summary: typeof data?.summary === "string" ? data.summary : "",
    disclaimer: typeof data?.disclaimer === "string" ? data.disclaimer : "",
  }
}

// ---------------------------------------------------------------------------
// Импорт образовательной программы: загрузка файла, черновик с проверкой,
// публикация и отклонение. Эндпоинтов пока нет в сгенерированной OpenAPI-схеме,
// формы фиксированы планом MVP, поэтому ниже ручные типы и те же raw-fetch
// обёртки, что у остальных MVP-карточек. 422 при разборе приходит как
// ApiRequestError с сообщением и предупреждениями в `warnings`.
// ---------------------------------------------------------------------------

/** Элемент KSA (знания, умения, навыки) внутри компетенции программы. */
export interface ProgramKsa {
  kind?: string
  title: string
  taxonomy_codes?: string[]
}

/** Компетенция импортированной программы вместе с элементами KSA. */
export interface ProgramCompetency {
  code?: string
  /**
   * Тип компетенции отдельным полем, а не частью кода: `universal` (УК),
   * `general_professional` (ОПК), `professional` (ПК) или `other` (иное).
   */
  type?: string
  title: string
  taxonomy_codes?: string[]
  ksa?: ProgramKsa[]
}

/** Программа в ответе импорта или черновика. */
export interface ImportedProgram {
  title: string
  code?: string
  organization?: string
  competencies?: ProgramCompetency[]
}

/** Отчёт разбора файла: сколько компетенций и KSA найдено и что стоит проверить. */
export interface ProgramImportReport {
  competencies: number
  ksa: number
  warnings: string[]
}

/** Ответ `POST /admin/programs/import` (201). */
export interface ProgramImportResponse {
  draft_id: string
  program: ImportedProgram
  report: ProgramImportReport
  status: string
}

/** Черновик в списке `GET /admin/programs/drafts`. */
export interface ProgramDraftSummary {
  id: string
  program_key: string
  source_name: string
  status: string
  created_at: string
  competencies: number
  ksa: number
}

/** Черновик с программой и отчётом (`GET /admin/programs/drafts/{id}`). */
export interface ProgramDraftDetail {
  id?: string
  program_key?: string
  source_name?: string
  status?: string
  created_at?: string
  program: ImportedProgram
  report?: ProgramImportReport
}

/** Ответ `POST /admin/programs/drafts/{id}/publish`. */
export interface ProgramPublishResult {
  program_id: string
  unmatched_competencies: number
  unmatched_ksa: number
  unknown_taxonomy_codes: string[]
}

/**
 * Загружает файл образовательной программы (CSV, XLSX, JSON или TXT) и создаёт
 * черновик. Ошибка разбора приходит как {@link ApiRequestError} со статусом 422,
 * `message` и массивом `warnings`.
 *
 * @param file Файл программы.
 * @param key Код программы, обязательное поле формы импорта.
 */
export async function importProgram(file: File, key: string): Promise<ProgramImportResponse> {
  const body = new FormData()
  body.append("file", file)
  body.append("key", key)
  return requestApi<ProgramImportResponse>("/admin/programs/import", { method: "POST", body })
}

/** Последние черновики программ; форма `{items: [...]}`, старый бэкенд мог отдать массив. */
export async function listProgramDrafts(): Promise<ProgramDraftSummary[]> {
  const data = await requestApi<ProgramDraftSummary[] | { items?: ProgramDraftSummary[] }>(
    "/admin/programs/drafts",
  )
  if (Array.isArray(data)) return data
  return Array.isArray(data?.items) ? data.items : []
}

/** Черновик программы с компетенциями и отчётом проверки. */
export async function getProgramDraft(id: string): Promise<ProgramDraftDetail> {
  return requestApi<ProgramDraftDetail>(`/admin/programs/drafts/${encodeURIComponent(id)}`)
}

/**
 * Публикует черновик программы.
 *
 * @param id Id черновика.
 * @param taxonomy Необязательный код или название таксономии, к которой привязать
 *   компетенции при публикации.
 */
export async function publishProgramDraft(
  id: string,
  taxonomy?: string,
): Promise<ProgramPublishResult> {
  return requestApi<ProgramPublishResult>(
    `/admin/programs/drafts/${encodeURIComponent(id)}/publish`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(taxonomy ? { taxonomy } : {}),
    },
  )
}

/** Отклоняет черновик программы; сервер отвечает 204, повторное решение даёт 409. */
export async function rejectProgramDraft(id: string): Promise<void> {
  await requestApi(`/admin/programs/drafts/${encodeURIComponent(id)}/reject`, { method: "POST" })
}

/** MIME-тип документа Word, который сервер отдаёт при выгрузке черновика. */
export const PROGRAM_DRAFT_DOCX_MIME =
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

/**
 * Убирает из имени файла пути, кавычки и управляющие символы, чтобы имя из
 * заголовка сервера нельзя было использовать как путь при сохранении.
 */
function sanitizeDownloadFilename(value: string): string {
  const segments = value.split(/[\\/]/)
  const base = segments.length > 0 ? segments[segments.length - 1] : value
  return stripControlChars(base).replace(/"/g, "").trim()
}

/** Декодирует значение `filename*` (RFC 5987) и возвращает безопасное имя. */
function decodeExtendedFilename(value: string): string | null {
  let decoded = value
  try {
    decoded = decodeURIComponent(value)
  } catch {
    // Некорректная процентная последовательность: используем имя как пришло.
  }
  const name = sanitizeDownloadFilename(decoded)
  return name || null
}

/**
 * Достаёт имя файла из `Content-Disposition`.
 *
 * Поддерживает `filename="program.docx"`, `filename=program.docx` и
 * RFC 5987 `filename*=UTF-8''program.docx`. Возвращает `null`, если заголовка
 * нет или имя после очистки пустое: вызывающий подставит fallback.
 */
export function parseContentDispositionFilename(header: string | null): string | null {
  if (!header) return null

  const extended = /filename\*\s*=\s*([^;]+)/i.exec(header)
  if (extended) {
    const value = extended[1].trim().replace(/^["']|["']$/g, "")
    const parts = /^[^']*'[^']*'(.*)$/.exec(value)
    const name = decodeExtendedFilename(parts ? parts[1] : value)
    if (name) return name
  }

  const plain = /filename\s*=\s*(?:"([^"]*)"|'([^']*)'|([^;]*))/i.exec(header)
  if (!plain) return null
  const raw = plain[1] ?? plain[2] ?? plain[3] ?? ""
  const name = sanitizeDownloadFilename(raw)
  return name || null
}

/**
 * Скачивает DOCX черновика программы
 * (`GET /admin/programs/drafts/{id}/export.docx`).
 *
 * Ответ - готовый файл, поэтому обёртка не использует `requestApi`: она не
 * разбирает JSON, а возвращает `Blob` и имя из `Content-Disposition`. Если
 * заголовка нет или имя пустое, имя будет `program-<id>.docx`. Ошибки приходят
 * как {@link ApiRequestError} со статусом (403, 404, 501): экран сам решает,
 * какой текст показать.
 *
 * @param id Id черновика.
 */
export async function downloadProgramDraftDocx(
  id: string,
): Promise<{ blob: Blob; filename: string }> {
  const path = `/admin/programs/drafts/${encodeURIComponent(id)}/export.docx`
  const headers = new Headers()
  headers.set("Accept", PROGRAM_DRAFT_DOCX_MIME)
  const token = getValidToken() ?? apiToken
  if (token) headers.set("Authorization", `Bearer ${token}`)

  let response: Response
  try {
    response = await fetch(`${apiBaseUrl}${path}`, { headers })
  } catch (error) {
    frontendLog.error("API request failed", { path, error: String(error) })
    throw new ApiRequestError("Сервер недоступен: проверьте соединение.", 0)
  }

  if (!response.ok) {
    let code: string | undefined
    let message = `Запрос завершился с HTTP ${response.status}`
    try {
      const body = (await response.json()) as ApiErrorBody
      if (typeof body?.code === "string") code = body.code
      if (typeof body?.message === "string" && body.message.trim()) {
        message = normalizeText(body.message)
      }
    } catch {
      // Тело не JSON (например, HTML страницы ошибки прокси): сообщение по статусу.
    }
    if (response.status >= 500) frontendLog.error("API 5xx", { path, status: response.status })
    else frontendLog.warn("API 4xx", { path, status: response.status })
    throw new ApiRequestError(message, response.status, code)
  }

  let blob: Blob
  try {
    blob = await response.blob()
  } catch {
    throw new ApiRequestError("Сервер вернул некорректный файл.", response.status)
  }

  const filename =
    parseContentDispositionFilename(response.headers.get("content-disposition")) ??
    `program-${id}.docx`
  return { blob, filename }
}

// ---------------------------------------------------------------------------
// Реестр источников образовательных программ: страницы с описанием программы,
// которые сервер проверяет по расписанию и при изменении создаёт черновик.
// Эндпоинтов пока нет в сгенерированной OpenAPI-схеме, формы фиксированы планом
// MVP, поэтому ниже ручные типы и те же raw-fetch обёртки, что у импорта.
// ---------------------------------------------------------------------------

/** Результат проверки источника: без изменений, новый черновик, нужен разбор файла или ошибка. */
export type ProgramWatchStatus = "unchanged" | "changed" | "needs_parser" | "needs_attention" | "error"

/**
 * Источник программы в реестре (`GET /admin/program-sources`).
 * `draft_id` заполняется, когда проверка создала черновик программы.
 */
export interface ProgramWatchSource {
  id: string
  program_key: string
  url: string
  doc_format?: string | null
  interval_hours: number
  enabled: boolean
  last_checked_at?: string | null
  last_status?: ProgramWatchStatus | null
  last_error?: string | null
  draft_id?: string | null
}

/** Ответ проверки источника (`POST /admin/program-sources/{id}/check`). */
export interface ProgramWatchCheckResult {
  status: ProgramWatchStatus
  draft_id?: string | null
  error?: string | null
}

/** Тело `POST /admin/program-sources`. */
export interface ProgramWatchSourceInput {
  program_key: string
  url: string
  interval_hours?: number
  doc_format?: string
}

/** Тело `PATCH /admin/program-sources/{id}`: включение и интервал проверки. */
export interface ProgramWatchSourceUpdate {
  enabled?: boolean
  interval_hours?: number
}

/** Последние источники программы; форма `{items: [...]}`, старый бэкенд мог отдать массив. */
export async function listProgramWatchSources(): Promise<ProgramWatchSource[]> {
  const data = await requestApi<ProgramWatchSource[] | { items?: ProgramWatchSource[] }>(
    "/admin/program-sources",
  )
  if (Array.isArray(data)) return data
  return Array.isArray(data?.items) ? data.items : []
}

/** Добавляет источник программы; строгий ключ проверяет сервер (400 или 409). */
export async function createProgramWatchSource(
  input: ProgramWatchSourceInput,
): Promise<ProgramWatchSource> {
  return requestApi<ProgramWatchSource>("/admin/program-sources", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  })
}

/** Включает или выключает источник, меняет интервал проверки. */
export async function updateProgramWatchSource(
  id: string,
  input: ProgramWatchSourceUpdate,
): Promise<ProgramWatchSource> {
  return requestApi<ProgramWatchSource>(`/admin/program-sources/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  })
}

/** Удаляет источник программы; сервер отвечает 204. */
export async function deleteProgramWatchSource(id: string): Promise<void> {
  await requestApi(`/admin/program-sources/${encodeURIComponent(id)}`, { method: "DELETE" })
}

/**
 * Запускает проверку источника сейчас. При изменении страницы сервер создаёт
 * черновик программы и возвращает его `draft_id`.
 */
export async function checkProgramWatchSource(id: string): Promise<ProgramWatchCheckResult> {
  return requestApi<ProgramWatchCheckResult>(
    `/admin/program-sources/${encodeURIComponent(id)}/check`,
    { method: "POST" },
  )
}
