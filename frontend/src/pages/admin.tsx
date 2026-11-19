/**
 * @file admin.tsx
 * @description Администрирование: исследования, LLM-метрики, расход токенов и материалы для агентов.
 */

import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react"
import { Link } from "react-router-dom"
import {
  ActivityIcon,
  ChevronDownIcon,
  DatabaseIcon,
  DownloadIcon,
  RefreshCwIcon,
  ShieldAlertIcon,
  Trash2Icon,
} from "lucide-react"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { StatusBadge } from "@/components/status-badge"
import { apiBaseUrl, apiClient, apiToken, type Run } from "@/lib/api/client"
import { getCurrentRole, getValidToken } from "@/lib/auth"
import { normalizeDashes } from "@/lib/text"
import { scoreSource, scoreTone, type SourceQualityItem } from "@/lib/source-quality"
import {
  averageHistogramMs,
  fetchMetricsText,
  parseMetrics,
  sumWhere,
  type MetricSample,
} from "@/lib/metrics"

interface LlmRequest {
  id: string
  agent: string
  model: string
  status: string
  error_code?: string
  latency_ms: number
  usage?: { total_tokens?: number }
  attempt?: number
  max_attempts?: number
  created_at?: string
}

interface PromptVersion {
  id: string
  agent: string
  version: string
  status: string
  created_at: string
  activated_at?: string
}

interface ModelUsage {
  model: string
  requests: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
}

interface AdminSource {
  id: string
  type: string
  title: string
  content?: string
}

const SOURCE_TYPE_LABELS: Record<string, string> = {
  vacancy: "Вакансия",
  interview: "Интервью",
  standard: "Профстандарт",
  document: "Документ",
}

const ROLE_LABEL: Record<string, string> = {
  admin: "Администратор",
  user: "Пользователь",
  specialist: "Специалист",
}

function scoreTextClass(total: number): string {
  const tone = scoreTone(total)
  if (tone === "success") return "text-success"
  if (tone === "warning") return "text-warning"
  return "text-destructive"
}

function scoreBarClass(total: number): string {
  const tone = scoreTone(total)
  if (tone === "success") return "bg-success"
  if (tone === "warning") return "bg-warning"
  return "bg-destructive"
}

function formatPercent(value: number): string {
  return `${value.toFixed(1)}%`
}

function formatMs(value: number | null): string {
  if (value === null) return "-"
  if (value >= 1000) return `${(value / 1000).toFixed(1)} с`
  return `${Math.round(value)} мс`
}

function Kpi({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <Card size="sm">
      <CardContent className="space-y-1">
        <p className="text-xs text-muted-foreground">{label}</p>
        <p className="text-2xl font-semibold tracking-tight">{value}</p>
        {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
      </CardContent>
    </Card>
  )
}

/** Администрирование: KPI, исследования, LLM-журнал, метрики агентов. */
export function Admin() {
  const role = getCurrentRole()
  const isAdmin = role === "admin"
  const [runs, setRuns] = useState<Run[]>([])
  const [health, setHealth] = useState<string | null>(null)
  const [metrics, setMetrics] = useState<MetricSample[]>([])
  const [metricsText, setMetricsText] = useState("")
  const [refreshing, setRefreshing] = useState(false)
  const [llmRequests, setLlmRequests] = useState<LlmRequest[]>([])
  const [llmUsage, setLlmUsage] = useState<ModelUsage[]>([])
  const [llmUsageError, setLlmUsageError] = useState(false)
  const [llmLoading, setLlmLoading] = useState(false)
  const [promptVersions, setPromptVersions] = useState<PromptVersion[]>([])
  const [activatingPrompt, setActivatingPrompt] = useState<string | null>(null)
  const [agentFilter, setAgentFilter] = useState("all")
  const [statusFilter, setStatusFilter] = useState("all")
  const [sources, setSources] = useState<AdminSource[]>([])
  const [sourceQuality, setSourceQuality] = useState<SourceQualityItem[]>([])
  const [materialsOpen, setMaterialsOpen] = useState(true)
  const [sourceType, setSourceType] = useState("document")
  const [sourceTitle, setSourceTitle] = useState("")
  const [sourceFile, setSourceFile] = useState<File | null>(null)
  const [sourceFileKey, setSourceFileKey] = useState(0)
  const [sourceSaving, setSourceSaving] = useState(false)
  const [indexingId, setIndexingId] = useState<string | null>(null)

  const loadSourceQuality = useCallback(async () => {
    try {
      const token = getValidToken() ?? apiToken
      const response = await fetch(`${apiBaseUrl}/admin/sources/quality`, {
        headers: { Authorization: `Bearer ${token}` },
      })
      if (!response.ok) throw new Error(`sources quality: ${response.status}`)
      const body = (await response.json()) as { items?: SourceQualityItem[] }
      setSourceQuality(body.items ?? [])
    } catch {
      setSourceQuality([])
    }
  }, [])

  const loadMetrics = useCallback(async () => {
    try {
      const text = await fetchMetricsText()
      setMetricsText(text)
      setMetrics(parseMetrics(text))
    } catch {
      setMetrics([])
    }
  }, [])

  const loadLlmRequests = useCallback(async () => {
    // Let the effect finish before flipping loading state; this avoids a
    // synchronous setState-in-effect lint error without suppressing the rule.
    await Promise.resolve()
    setLlmLoading(true)
    try {
      const params = new URLSearchParams({ limit: "10" })
      if (agentFilter !== "all") params.set("agent", agentFilter)
      if (statusFilter !== "all") params.set("status", statusFilter)
      const token = getValidToken() ?? apiToken
      const headers = token ? { Authorization: `Bearer ${token}` } : undefined
      const [requestsResponse, usageResponse] = await Promise.all([
        fetch(`${apiBaseUrl}/llm/requests?${params.toString()}`, { headers }),
        fetch(`${apiBaseUrl}/llm/usage?since=24h`, { headers }),
      ])

      if (requestsResponse.ok) {
        const payload = (await requestsResponse.json()) as { items?: LlmRequest[] }
        setLlmRequests(normalizeDashes(payload.items ?? []))
      } else {
        setLlmRequests([])
      }

      if (usageResponse.ok) {
        const payload = (await usageResponse.json()) as { items?: ModelUsage[] }
        setLlmUsage(normalizeDashes(payload.items ?? []))
        setLlmUsageError(false)
      } else {
        setLlmUsage([])
        setLlmUsageError(true)
      }
    } catch {
      setLlmRequests([])
      setLlmUsage([])
      setLlmUsageError(true)
    } finally {
      setLlmLoading(false)
    }
  }, [agentFilter, statusFilter])

  const loadPromptVersions = useCallback(async () => {
    try {
      const token = getValidToken() ?? apiToken
      const response = await fetch(`${apiBaseUrl}/llm/prompt-versions`, {
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      })
      if (!response.ok) {
        setPromptVersions([])
        return
      }
      const payload = (await response.json()) as { items?: PromptVersion[] }
      setPromptVersions(normalizeDashes(payload.items ?? []))
    } catch {
      setPromptVersions([])
    }
  }, [])

  useEffect(() => {
    if (!isAdmin) return
    let cancelled = false
    const load = async () => {
      const [runsResponse, healthResponse, sourcesResponse] = await Promise.all([
        apiClient.GET("/runs"),
        apiClient.GET("/health"),
        apiClient.GET("/sources"),
      ])
      await loadMetrics()
      await loadPromptVersions()
      await loadSourceQuality()
      if (cancelled) return
      if (runsResponse.data) setRuns(runsResponse.data)
      if (sourcesResponse.data) setSources(sourcesResponse.data)
      setHealth(healthResponse.data?.status ?? "недоступен")
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [isAdmin, loadMetrics, loadPromptVersions, loadSourceQuality])
  useEffect(() => {
    if (!isAdmin) return
    const timer = window.setTimeout(() => {
      void loadLlmRequests()
    }, 0)
    return () => window.clearTimeout(timer)
  }, [isAdmin, loadLlmRequests])

  const refresh = async () => {
    setRefreshing(true)
    await loadMetrics()
    await loadPromptVersions()
    await loadSourceQuality()
    await loadLlmRequests()
    setRefreshing(false)
  }

  const activatePromptVersion = async (version: PromptVersion) => {
    setActivatingPrompt(version.id)
    try {
      const token = getValidToken() ?? apiToken
      const response = await fetch(`${apiBaseUrl}/llm/prompt-versions/activate`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify({ id: version.id, reason: "Активация из админки" }),
      })
      if (!response.ok) {
        toast.error("Не удалось активировать версию промпта")
        return
      }
      toast.success("Версия промпта активирована")
      await loadPromptVersions()
    } catch {
      toast.error("Сервер недоступен", { description: "Версия промпта не активирована." })
    } finally {
      setActivatingPrompt(null)
    }
  }

  const addSource = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!sourceFile || sourceSaving) return
    setSourceSaving(true)
    try {
      const form = new FormData()
      form.append("file", sourceFile)
      form.append("type", sourceType)
      if (sourceTitle.trim()) form.append("title", sourceTitle.trim())

      const token = getValidToken() ?? apiToken
      const response = await fetch(`${apiBaseUrl}/sources/upload`, {
        method: "POST",
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
        body: form,
      })
      if (!response.ok) {
        const payload = (await response.json().catch(() => null)) as { message?: string } | null
        toast.error("Не удалось загрузить файл", {
          description: payload?.message ?? `HTTP ${response.status}`,
        })
        return
      }
      const created = normalizeDashes((await response.json()) as AdminSource)
      setSourceTitle("")
      setSourceFile(null)
      setSourceFileKey((value) => value + 1)
      setSources((prev) => [created, ...prev])
      toast.success("Материал загружен", {
        description: "Агенты будут учитывать его в следующих исследованиях.",
      })
    } catch {
      toast.error("Сервер недоступен", { description: "Файл не загружен." })
    } finally {
      setSourceSaving(false)
    }
  }

  const indexSource = async (sourceId: string) => {
    setIndexingId(sourceId)
    try {
      const { error } = await apiClient.POST("/sources/{id}/index", {
        params: { path: { id: sourceId } },
      })
      if (error) {
        toast.error("Не удалось проиндексировать материал", { description: error.message })
        return
      }
      toast.success("Материал проиндексирован")
    } catch {
      toast.error("Сервер недоступен", { description: "Индексация не запущена." })
    } finally {
      setIndexingId(null)
    }
  }

  const removeSource = async (sourceId: string) => {
    try {
      const { error } = await apiClient.DELETE("/sources/{id}", { params: { path: { id: sourceId } } })
      if (error) {
        toast.error("Не удалось удалить материал", { description: error.message })
        return
      }
      setSources((prev) => prev.filter((item) => item.id !== sourceId))
      toast.success("Материал удалён")
    } catch {
      toast.error("Сервер недоступен", { description: "Материал не удалён." })
    }
  }

  const stats = useMemo(() => {
    const total = runs.length
    const running = runs.filter((run) => run.status === "running").length
    const done = runs.filter((run) => run.status === "done").length
    const failed = runs.filter((run) => run.status === "failed").length

    const httpTotal = sumWhere(metrics, "activity_descriptor_http_requests_total")
    const http4xx = sumWhere(
      metrics,
      "activity_descriptor_http_requests_total",
      (sample) => (sample.labels.status ?? "").startsWith("4"),
    )
    const http5xx = sumWhere(
      metrics,
      "activity_descriptor_http_requests_total",
      (sample) => (sample.labels.status ?? "").startsWith("5"),
    )
    const apiLatency = averageHistogramMs(
      metrics,
      "activity_descriptor_http_request_duration_seconds",
      (sample) => !(sample.labels.route ?? "").endsWith("/events"),
    )
    const runLatency = averageHistogramMs(metrics, "activity_descriptor_run_duration_seconds")

    return { total, running, done, failed, httpTotal, http4xx, http5xx, apiLatency, runLatency }
  }, [runs, metrics])

  const agents = useMemo(() => {
    const callsName = "activity_descriptor_agent_calls_total"
    const names = Array.from(
      new Set(metrics.filter((sample) => sample.name === callsName).map((sample) => sample.labels.agent ?? "unknown")),
    )
    return names.map((agent) => ({
      agent,
      calls: sumWhere(metrics, callsName, (sample) => (sample.labels.agent ?? "unknown") === agent),
      errors: sumWhere(
        metrics,
        "activity_descriptor_agent_errors_total",
        (sample) => (sample.labels.agent ?? "unknown") === agent,
      ),
      avgMs: averageHistogramMs(
        metrics,
        "activity_descriptor_agent_latency_seconds",
        (sample) => (sample.labels.agent ?? "unknown") === agent,
      ),
    }))
  }, [metrics])

  const llmErrors = useMemo(() => {
    const counts = new Map<string, number>()
    for (const request of llmRequests) {
      if (request.status === "ok") continue
      const code = request.error_code || request.status
      counts.set(code, (counts.get(code) ?? 0) + 1)
    }
    return Array.from(counts.entries()).sort((left, right) => right[1] - left[1])
  }, [llmRequests])

  const tokenStats = useMemo(() => {
    const total = llmUsage.reduce((sum, item) => sum + item.total_tokens, 0)
    const requests = llmUsage.reduce((sum, item) => sum + item.requests, 0)
    return { total, requests }
  }, [llmUsage])

  const sourceStats = useMemo(() => {
    const byType = new Map<string, number>()
    let withContent = 0
    for (const source of sources) {
      byType.set(source.type, (byType.get(source.type) ?? 0) + 1)
      if (source.content && source.content.trim() !== "") withContent++
    }
    return {
      byType: Array.from(byType.entries()).sort((left, right) => right[1] - left[1]),
      withContent,
      empty: sources.length - withContent,
    }
  }, [sources])

  const scoredSources = useMemo(
    () =>
      sourceQuality
        .map((item) => ({ item, score: scoreSource(item) }))
        .sort((left, right) => right.score.total - left.score.total),
    [sourceQuality],
  )
  const averageSourceScore =
    scoredSources.length > 0
      ? Math.round(
          scoredSources.reduce((total, entry) => total + entry.score.total, 0) /
            scoredSources.length,
        )
      : null

  const routeStats = useMemo(() => {
    const map = new Map<
      string,
      {
        route: string
        requests: number
        errors4xx: number
        errors5xx: number
        errors: Record<string, number>
        sum: number
        count: number
      }
    >()
    for (const sample of metrics) {
      const route = sample.labels.route
      if (!route) continue
      const entry =
        map.get(route) ?? {
          route,
          requests: 0,
          errors4xx: 0,
          errors5xx: 0,
          errors: {},
          sum: 0,
          count: 0,
        }
      if (sample.name === "activity_descriptor_http_requests_total") {
        entry.requests += sample.value
        const status = sample.labels.status ?? ""
        if (status.startsWith("4") || status.startsWith("5")) {
          const method = sample.labels.method ?? "?"
          const key = `${method} ${status}`
          entry.errors[key] = (entry.errors[key] ?? 0) + sample.value
          if (status.startsWith("5")) entry.errors5xx += sample.value
          if (status.startsWith("4")) entry.errors4xx += sample.value
        }
      } else if (sample.name === "activity_descriptor_http_request_duration_seconds_sum") {
        entry.sum += sample.value
      } else if (sample.name === "activity_descriptor_http_request_duration_seconds_count") {
        entry.count += sample.value
      } else {
        continue
      }
      map.set(route, entry)
    }
    return Array.from(map.values())
      .map((entry) => ({
        ...entry,
        avgMs: entry.count > 0 ? (entry.sum / entry.count) * 1000 : null,
      }))
      .sort((left, right) => right.requests - left.requests)
      .slice(0, 8)
  }, [metrics])

  const downloadMetrics = () => {
    const blob = new Blob([metricsText], { type: "text/plain;charset=utf-8" })
    const url = URL.createObjectURL(blob)
    const link = document.createElement("a")
    link.href = url
    link.download = "metrics.prom"
    link.click()
    URL.revokeObjectURL(url)
  }

  if (!isAdmin) {
    return (
      <div className="mx-auto max-w-xl">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <ShieldAlertIcon className="size-5 text-destructive" />
              Доступ только для администратора
            </CardTitle>
            <CardDescription>
              Текущая роль: {role ? (ROLE_LABEL[role] ?? role) : "не авторизован"}. Войдите с ролью
              admin, чтобы открыть админку.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button asChild>
              <Link to="/login">Перейти ко входу</Link>
            </Button>
          </CardContent>
        </Card>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="space-y-1">
          <h1 className="text-3xl font-semibold tracking-tight">Администрирование</h1>
        </div>
        <Button variant="outline" onClick={() => void refresh()} disabled={refreshing}>
          <RefreshCwIcon className={refreshing ? "size-4 animate-spin" : "size-4"} />
          Обновить
        </Button>
      </div>

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        <Kpi label="Исследования" value={String(stats.total)} hint={`интервью: ${stats.running}`} />
        <Kpi label="Завершено / провалено" value={`${stats.done} / ${stats.failed}`} hint="по статусам исследований" />
        <Kpi
          label="HTTP 4xx"
          value={stats.httpTotal > 0 ? formatPercent((stats.http4xx / stats.httpTotal) * 100) : "-"}
          hint={`${stats.http4xx} из ${stats.httpTotal} запросов`}
        />
        <Kpi
          label="HTTP 5xx"
          value={stats.httpTotal > 0 ? formatPercent((stats.http5xx / stats.httpTotal) * 100) : "-"}
          hint={`${stats.http5xx} из ${stats.httpTotal} запросов`}
        />
        <Kpi
          label="Средняя латентность API"
          value={formatMs(stats.apiLatency)}
          hint={stats.runLatency !== null ? `исследование: ${formatMs(stats.runLatency)}` : "без учёта SSE"}
        />
        <Kpi
          label="Токенов за 24 часа"
          value={llmUsageError ? "-" : tokenStats.total.toLocaleString("ru-RU")}
          hint={
            llmUsageError
              ? "нет доступа к расходу"
              : `${tokenStats.requests.toLocaleString("ru-RU")} запросов к моделям`
          }
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Версии промптов</CardTitle>
          <CardDescription>
            Версии системных промптов агентов; активная версия используется новыми запросами.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {promptVersions.length === 0 ? (
            <p className="text-sm text-muted-foreground">Версий промптов пока нет.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead className="text-xs text-muted-foreground">
                  <tr className="border-b">
                    <th className="py-2 pr-3 font-medium">Агент</th>
                    <th className="py-2 pr-3 font-medium">Версия</th>
                    <th className="py-2 pr-3 font-medium">Статус</th>
                    <th className="py-2 pr-3 font-medium">Создана</th>
                    <th className="py-2 font-medium" />
                  </tr>
                </thead>
                <tbody>
                  {promptVersions.map((version) => (
                    <tr key={version.id} className="border-b last:border-b-0">
                      <td className="py-2 pr-3">{version.agent}</td>
                      <td className="py-2 pr-3 font-mono text-xs">{version.version}</td>
                      <td className="py-2 pr-3">
                        <Badge variant={version.status === "active" ? "default" : "secondary"}>
                          {version.status === "active" ? "активна" : version.status}
                        </Badge>
                      </td>
                      <td className="py-2 pr-3 text-xs text-muted-foreground">
                        {version.created_at ? new Date(version.created_at).toLocaleString("ru-RU") : "-"}
                      </td>
                      <td className="py-2 text-right">
                        {version.status !== "active" && (
                          <Button
                            variant="outline"
                            size="sm"
                            className="h-7 text-xs"
                            disabled={activatingPrompt === version.id}
                            onClick={() => void activatePromptVersion(version)}
                          >
                            Активировать
                          </Button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <ActivityIcon className="size-4 shrink-0" />
            LLM-запросы
          </CardTitle>
          <CardDescription>Последние 10 обращений к модели: статус, токены, ошибки.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-wrap gap-2">
            <Select value={agentFilter} onValueChange={setAgentFilter}>
              <SelectTrigger className="w-44" aria-label="Фильтр по агенту">
                <SelectValue placeholder="Агент" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">Все агенты</SelectItem>
                <SelectItem value="interviewer">Интервьюер</SelectItem>
                <SelectItem value="analyst">Аналитик</SelectItem>
                <SelectItem value="synthesizer">Синтезатор</SelectItem>
              </SelectContent>
            </Select>
            <Select value={statusFilter} onValueChange={setStatusFilter}>
              <SelectTrigger className="w-44" aria-label="Фильтр по статусу">
                <SelectValue placeholder="Статус" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">Все статусы</SelectItem>
                <SelectItem value="ok">ok</SelectItem>
                <SelectItem value="error">error</SelectItem>
                <SelectItem value="timeout">timeout</SelectItem>
              </SelectContent>
            </Select>
          </div>

          {llmLoading ? (
            <div className="space-y-2">
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
            </div>
          ) : llmRequests.length === 0 ? (
            <p className="text-sm text-muted-foreground">Запросов нет.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead className="text-xs text-muted-foreground">
                  <tr className="border-b">
                    <th className="py-2 pr-3 font-medium">Время</th>
                    <th className="py-2 pr-3 font-medium">Агент</th>
                    <th className="py-2 pr-3 font-medium">Модель</th>
                    <th className="py-2 pr-3 font-medium">Статус</th>
                    <th className="py-2 pr-3 font-medium">Токены</th>
                    <th className="py-2 font-medium">Ошибка</th>
                  </tr>
                </thead>
                <tbody>
                  {llmRequests.map((request) => (
                    <tr key={request.id} className="border-b last:border-b-0">
                      <td className="py-2 pr-3 text-muted-foreground">
                        {request.created_at
                          ? new Intl.DateTimeFormat("ru-RU", { timeStyle: "medium" }).format(
                              new Date(request.created_at),
                            )
                          : "-"}
                      </td>
                      <td className="py-2 pr-3">{request.agent}</td>
                      <td className="py-2 pr-3 text-muted-foreground">{request.model}</td>
                      <td className="py-2 pr-3">
                        <Badge variant={request.status === "ok" ? "secondary" : "destructive"}>
                          {request.status}
                        </Badge>
                      </td>
                      <td className="py-2 pr-3">
                        {request.usage?.total_tokens
                          ? request.usage.total_tokens.toLocaleString("ru-RU")
                          : "-"}
                      </td>
                      <td className="py-2 text-muted-foreground">{request.error_code || "-"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Токены по моделям</CardTitle>
          <CardDescription>Расход за последние 24 часа: запросы и токены.</CardDescription>
        </CardHeader>
        <CardContent>
          {llmUsageError ? (
            <p className="text-sm text-destructive">
              Не удалось загрузить расход токенов. Проверьте, что вы вошли с ролью admin и бэкенд
              отвечает на /api/v1/llm/usage.
            </p>
          ) : llmUsage.length === 0 ? (
            <p className="text-sm text-muted-foreground">Данных о расходе пока нет.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead className="text-xs text-muted-foreground">
                  <tr className="border-b">
                    <th className="py-2 pr-3 font-medium">Модель</th>
                    <th className="py-2 pr-3 font-medium">Запросы</th>
                    <th className="py-2 pr-3 font-medium">Промпт</th>
                    <th className="py-2 pr-3 font-medium">Ответ</th>
                    <th className="py-2 font-medium">Всего токенов</th>
                  </tr>
                </thead>
                <tbody>
                  {llmUsage.map((usage) => (
                    <tr key={usage.model} className="border-b last:border-b-0">
                      <td className="py-2 pr-3 font-mono text-xs">{usage.model}</td>
                      <td className="py-2 pr-3">{usage.requests}</td>
                      <td className="py-2 pr-3">{usage.prompt_tokens.toLocaleString("ru-RU")}</td>
                      <td className="py-2 pr-3">{usage.completion_tokens.toLocaleString("ru-RU")}</td>
                      <td className="py-2 font-medium">{usage.total_tokens.toLocaleString("ru-RU")}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Ошибки LLM</CardTitle>
          <CardDescription>Коды ошибок в загруженных запросах.</CardDescription>
        </CardHeader>
        <CardContent>
          {llmErrors.length === 0 ? (
            <p className="text-sm text-muted-foreground">Ошибок нет.</p>
          ) : (
            <ul className="divide-y">
              {llmErrors.map(([code, count]) => (
                <li key={code} className="flex items-center justify-between gap-3 py-2 text-sm">
                  <span className="font-mono text-xs">{code}</span>
                  <span className="text-muted-foreground">{count}</span>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Метрики агентов</CardTitle>
          <CardDescription>Вызовы, ошибки и средняя латентность по агентам.</CardDescription>
        </CardHeader>
        <CardContent>
          {agents.length === 0 ? (
            <p className="text-sm text-muted-foreground">Метрик агентов пока нет.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead className="text-xs text-muted-foreground">
                  <tr className="border-b">
                    <th className="py-2 pr-3 font-medium">Агент</th>
                    <th className="py-2 pr-3 font-medium">Вызовы</th>
                    <th className="py-2 pr-3 font-medium">Ошибки</th>
                    <th className="py-2 font-medium">Средняя латентность</th>
                  </tr>
                </thead>
                <tbody>
                  {agents.map((agent) => (
                    <tr key={agent.agent} className="border-b last:border-b-0">
                      <td className="py-2 pr-3">{agent.agent}</td>
                      <td className="py-2 pr-3">{agent.calls}</td>
                      <td className="py-2 pr-3">{agent.errors}</td>
                      <td className="py-2">{formatMs(agent.avgMs)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Качество источников</CardTitle>
          <CardDescription>
            Оценка по типу, свежести, объёму, извлечению текста, происхождению, цитированию и
            подтверждению цитат.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {sources.length === 0 ? (
            <p className="text-sm text-muted-foreground">Источников пока нет.</p>
          ) : (
            <>
              <div className="flex flex-wrap gap-2">
                {sourceStats.byType.map(([type, count]) => (
                  <Badge key={type} variant="secondary">
                    {SOURCE_TYPE_LABELS[type] ?? type}: {count}
                  </Badge>
                ))}
              </div>
              {scoredSources.length > 0 ? (
                <>
                  <div className="flex items-end justify-between gap-3">
                    <div>
                      <p className="text-xs text-muted-foreground">Средняя оценка</p>
                      <p
                        className={`text-2xl font-semibold tabular-nums ${scoreTextClass(averageSourceScore ?? 0)}`}
                      >
                        {averageSourceScore}
                        <span className="ml-1 text-sm font-normal text-muted-foreground">/ 100</span>
                      </p>
                    </div>
                    <span className="text-xs text-muted-foreground">
                      источников: {scoredSources.length}
                    </span>
                  </div>
                  <div className="h-2 overflow-hidden rounded-full bg-muted">
                    <div
                      className={`h-full rounded-full ${scoreBarClass(averageSourceScore ?? 0)}`}
                      style={{ width: `${averageSourceScore ?? 0}%` }}
                    />
                  </div>
                  <p className="text-[11px] leading-relaxed text-muted-foreground">
                    Тип 20 · Свежесть 15 · Объём 15 · Текст 10 · Происхождение 10 · Цитирование 20 ·
                    Цитаты 10
                  </p>
                  <ul className="max-h-72 divide-y overflow-y-auto">
                    {scoredSources.map(({ item, score }) => (
                      <li
                        key={item.id}
                        className="flex items-center gap-2 py-2 text-sm"
                        title={`Тип ${score.authority}/20 · свежесть ${score.freshness}/15 · объём ${score.volume}/15 · текст ${score.extraction}/10 · происхождение ${score.origin}/10 · цитаты ${score.citations}/20 · подтверждение ${score.quotes}/10`}
                      >
                        <span
                          className={`w-8 shrink-0 text-right font-semibold tabular-nums ${scoreTextClass(score.total)}`}
                        >
                          {score.total}
                        </span>
                        <span className="min-w-0 flex-1 truncate">{item.title}</span>
                        <Badge variant="secondary">
                          {SOURCE_TYPE_LABELS[item.type] ?? item.type}
                        </Badge>
                        <span className="shrink-0 text-xs text-muted-foreground">
                          {item.citations} цит.
                        </span>
                      </li>
                    ))}
                  </ul>
                </>
              ) : (
                <div className="space-y-1">
                  <div className="flex justify-between text-xs text-muted-foreground">
                    <span>С извлечённым текстом</span>
                    <span>
                      {sourceStats.withContent} из {sources.length}
                    </span>
                  </div>
                  <div className="h-2 overflow-hidden rounded-full bg-muted">
                    <div
                      className="h-full rounded-full bg-primary"
                      style={{ width: `${(sourceStats.withContent / sources.length) * 100}%` }}
                    />
                  </div>
                  {sourceStats.empty > 0 && (
                    <p className="text-xs text-warning">Без текста: {sourceStats.empty}</p>
                  )}
                </div>
              )}
            </>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader className="flex flex-row items-start justify-between gap-3">
          <div className="space-y-1.5">
            <CardTitle className="text-base">Материалы для агентов</CardTitle>
            <CardDescription>
              Загрузите PDF, DOCX или текстовый файл - текст извлечётся автоматически. Материалы
              доступны всем новым исследованиям, агенты опираются на них при разборе интервью.
            </CardDescription>
          </div>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="shrink-0 text-muted-foreground"
            aria-expanded={materialsOpen}
            aria-controls="materials-content"
            aria-label={materialsOpen ? "Свернуть материалы" : "Развернуть материалы"}
            onClick={() => setMaterialsOpen((open) => !open)}
          >
            <ChevronDownIcon
              className={`size-4 transition-transform ${materialsOpen ? "rotate-180" : ""}`}
            />
          </Button>
        </CardHeader>
        {materialsOpen && (
        <CardContent id="materials-content" className="space-y-4">
          <form onSubmit={addSource} className="space-y-3">
            <div className="grid gap-3 sm:grid-cols-[180px_1fr]">
              <div className="space-y-2">
                <Label htmlFor="material-type">Тип</Label>
                <Select value={sourceType} onValueChange={setSourceType}>
                  <SelectTrigger id="material-type">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {Object.entries(SOURCE_TYPE_LABELS).map(([value, label]) => (
                      <SelectItem key={value} value={value}>
                        {label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-2">
                <Label htmlFor="material-title">Название (необязательно)</Label>
                <Input
                  id="material-title"
                  value={sourceTitle}
                  onChange={(event) => setSourceTitle(event.target.value)}
                  placeholder="По умолчанию - имя файла"
                  maxLength={200}
                />
              </div>
            </div>
            <div className="space-y-2">
              <Label htmlFor="material-file">Файл</Label>
              <Input
                key={sourceFileKey}
                id="material-file"
                type="file"
                accept=".pdf,.docx,.txt,.md,.markdown,.csv,.json"
                onChange={(event) => setSourceFile(event.target.files?.[0] ?? null)}
              />
              <p className="text-xs text-muted-foreground">
                PDF, DOCX, TXT, MD, CSV или JSON · до 20 МБ. Текст извлекается автоматически.
              </p>
            </div>
            <Button type="submit" size="sm" disabled={!sourceFile || sourceSaving}>
              {sourceSaving ? "Загрузка…" : "Загрузить файл"}
            </Button>
          </form>

          {sources.length === 0 ? (
            <p className="text-sm text-muted-foreground">Материалов пока нет.</p>
          ) : (
            <ul className="max-h-80 divide-y overflow-y-auto">
              {sources.map((source) => (
                <li key={source.id} className="flex items-center gap-3 py-3 text-sm">
                  <Badge variant="secondary">{SOURCE_TYPE_LABELS[source.type] ?? source.type}</Badge>
                  <span className="min-w-0 flex-1 truncate">{source.title}</span>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={`Проиндексировать материал ${source.title}`}
                    className="shrink-0 text-muted-foreground"
                    disabled={indexingId === source.id}
                    onClick={() => void indexSource(source.id)}
                  >
                    <DatabaseIcon className="size-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={`Удалить материал ${source.title}`}
                    className="shrink-0 text-muted-foreground hover:text-destructive"
                    onClick={() => void removeSource(source.id)}
                  >
                    <Trash2Icon className="size-4" />
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
        )}
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">HTTP по маршрутам</CardTitle>
          <CardDescription>
            Запросы, конкретные коды ошибок 4xx/5xx и средняя латентность.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {routeStats.length === 0 ? (
            <p className="text-sm text-muted-foreground">Метрик пока нет.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead className="text-xs text-muted-foreground">
                  <tr className="border-b">
                    <th className="py-2 pr-3 font-medium">Маршрут</th>
                    <th className="py-2 pr-3 font-medium">Запросы</th>
                    <th className="py-2 pr-3 font-medium">4xx</th>
                    <th className="py-2 pr-3 font-medium">5xx</th>
                    <th className="py-2 font-medium">Средняя латентность</th>
                  </tr>
                </thead>
                <tbody>
                  {routeStats.map((entry) => (
                    <tr key={entry.route} className="border-b last:border-b-0">
                      <td className="py-2 pr-3 font-mono text-xs">{entry.route}</td>
                      <td className="py-2 pr-3">{entry.requests}</td>
                      <td className="py-2 pr-3">
                        {entry.errors4xx === 0 ? (
                          "0"
                        ) : (
                          <div className="flex flex-wrap gap-1">
                            {Object.entries(entry.errors)
                              .filter(([key]) => key.split(" ").at(-1)?.startsWith("4"))
                              .map(([key, count]) => (
                                <Badge
                                  key={key}
                                  variant="outline"
                                  className="px-1.5 py-0 font-mono text-[10px] text-warning"
                                >
                                  {key} ×{count}
                                </Badge>
                              ))}
                          </div>
                        )}
                      </td>
                      <td className="py-2 pr-3">
                        {entry.errors5xx === 0 ? (
                          "0"
                        ) : (
                          <div className="flex flex-wrap gap-1">
                            {Object.entries(entry.errors)
                              .filter(([key]) => key.split(" ").at(-1)?.startsWith("5"))
                              .map(([key, count]) => (
                                <Badge
                                  key={key}
                                  variant="outline"
                                  className="px-1.5 py-0 font-mono text-[10px] text-destructive"
                                >
                                  {key} ×{count}
                                </Badge>
                              ))}
                          </div>
                        )}
                      </td>
                      <td className="py-2">{formatMs(entry.avgMs)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Исследования</CardTitle>
          <CardDescription>
            Всего: {runs.length}. Только просмотр - удалить исследование может его автор на главной
            странице.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {runs.length === 0 ? (
            <p className="text-sm text-muted-foreground">Исследований нет.</p>
          ) : (
            <ul className="divide-y">
              {runs.map((run) => (
                <li key={run.id} className="flex items-center gap-3 py-3">
                  <div className="min-w-0 flex-1">
                    <Link to={`/research/${run.id}`} className="block truncate font-medium hover:underline">
                      {run.profession}
                    </Link>
                  </div>
                  <StatusBadge status={run.status} />
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Состояние сервиса</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-wrap items-center gap-3 text-sm">
          <span>
            API: <span className="font-medium">{health ?? "проверяется…"}</span>
          </span>
          <Button variant="outline" size="sm" onClick={downloadMetrics} disabled={!metricsText}>
            <DownloadIcon className="size-4" />
            Скачать /metrics
          </Button>
        </CardContent>
      </Card>
    </div>
  )
}
