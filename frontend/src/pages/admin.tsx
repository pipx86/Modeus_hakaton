import { useCallback, useEffect, useMemo, useState } from "react"
import { Link } from "react-router-dom"
import {
  ActivityIcon,
  DownloadIcon,
  RefreshCwIcon,
  ShieldAlertIcon,
  Trash2Icon,
  UsersIcon,
} from "lucide-react"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { StatusBadge } from "@/components/status-badge"
import { Sparkline } from "@/components/charts"
import { apiBaseUrl, apiClient, apiToken, type Run } from "@/lib/api/client"
import { decodeToken, getCurrentRole, getValidToken } from "@/lib/auth"
import {
  averageHistogramMs,
  fetchMetricsText,
  parseMetrics,
  sumWhere,
  type MetricSample,
} from "@/lib/metrics"

interface DemoUser {
  subject: string
  role: string
  read_only?: boolean
}

interface LlmRequest {
  id: string
  agent: string
  model: string
  status: string
  error_code?: string
  latency_ms: number
  attempt?: number
  max_attempts?: number
  created_at?: string
}

const ROLE_LABEL: Record<string, string> = {
  admin: "Администратор",
  guest: "Гость",
  teacher: "Преподаватель",
  rop: "РОП",
  student: "Студент",
}

function formatPercent(value: number): string {
  return `${value.toFixed(1)}%`
}

function formatMs(value: number | null): string {
  if (value === null) return "—"
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

/** Админка: KPI, пользователи, исследования, LLM-журнал, метрики агентов. */
export function Admin() {
  const role = getCurrentRole()
  const isAdmin = role === "admin"
  const [users, setUsers] = useState<DemoUser[]>([])
  const [runs, setRuns] = useState<Run[]>([])
  const [health, setHealth] = useState<string | null>(null)
  const [metrics, setMetrics] = useState<MetricSample[]>([])
  const [metricsText, setMetricsText] = useState("")
  const [loading, setLoading] = useState(isAdmin)
  const [refreshing, setRefreshing] = useState(false)
  const [deletingId, setDeletingId] = useState<string | null>(null)
  const [llmRequests, setLlmRequests] = useState<LlmRequest[]>([])
  const [llmLoading, setLlmLoading] = useState(false)
  const [agentFilter, setAgentFilter] = useState("all")
  const [statusFilter, setStatusFilter] = useState("all")

  const session = (() => {
    const token = getValidToken()
    return token ? decodeToken(token) : null
  })()

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
      const params = new URLSearchParams({ limit: "50" })
      if (agentFilter !== "all") params.set("agent", agentFilter)
      if (statusFilter !== "all") params.set("status", statusFilter)
      const token = getValidToken() ?? apiToken
      const response = await fetch(`${apiBaseUrl}/llm/requests?${params.toString()}`, {
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      })
      if (!response.ok) throw new Error(String(response.status))
      const payload = (await response.json()) as { items?: LlmRequest[] }
      setLlmRequests(payload.items ?? [])
    } catch {
      setLlmRequests([])
    } finally {
      setLlmLoading(false)
    }
  }, [agentFilter, statusFilter])

  useEffect(() => {
    if (!isAdmin) return
    let cancelled = false
    const load = async () => {
      const [usersResponse, runsResponse, healthResponse] = await Promise.all([
        apiClient.GET("/users"),
        apiClient.GET("/runs"),
        apiClient.GET("/health"),
      ])
      await loadMetrics()
      if (cancelled) return
      if (usersResponse.data) setUsers(usersResponse.data)
      if (runsResponse.data) setRuns(runsResponse.data)
      setHealth(healthResponse.data?.status ?? "недоступен")
      setLoading(false)
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [isAdmin, loadMetrics])

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
    await loadLlmRequests()
    setRefreshing(false)
  }

  const removeRun = async (run: Run) => {
    setDeletingId(run.id)
    try {
      const { error } = await apiClient.DELETE("/runs/{id}", { params: { path: { id: run.id } } })
      if (error) {
        toast.error("Не удалось удалить исследование", { description: error.message })
        return
      }
      setRuns((prev) => prev.filter((item) => item.id !== run.id))
      toast.success("Исследование удалено")
    } catch {
      toast.error("Сервер недоступен", { description: "Исследование не удалено." })
    } finally {
      setDeletingId(null)
    }
  }

  const stats = useMemo(() => {
    const total = runs.length
    const running = runs.filter((run) => run.status === "running").length
    const done = runs.filter((run) => run.status === "done").length
    const failed = runs.filter((run) => run.status === "failed").length

    const httpTotal = sumWhere(metrics, "activity_descriptor_http_requests_total")
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

    return { total, running, done, failed, httpTotal, http5xx, apiLatency, runLatency }
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

  const llmLatencies = useMemo(
    () => llmRequests.map((request) => request.latency_ms).reverse(),
    [llmRequests],
  )

  const llmLatencyStats = useMemo(() => {
    if (llmLatencies.length === 0) return null
    const min = Math.min(...llmLatencies)
    const max = Math.max(...llmLatencies)
    const avg = llmLatencies.reduce((sum, value) => sum + value, 0) / llmLatencies.length
    return { min, max, avg }
  }, [llmLatencies])

  const routeStats = useMemo(() => {
    const map = new Map<string, { route: string; requests: number; errors: number; sum: number; count: number }>()
    for (const sample of metrics) {
      const route = sample.labels.route
      if (!route) continue
      const entry =
        map.get(route) ?? { route, requests: 0, errors: 0, sum: 0, count: 0 }
      if (sample.name === "activity_descriptor_http_requests_total") {
        entry.requests += sample.value
        if ((sample.labels.status ?? "").startsWith("5")) entry.errors += sample.value
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
          <h1 className="text-3xl font-semibold tracking-tight">Админка</h1>
          <p className="text-sm text-muted-foreground">
            {session?.sub ?? "admin"} - управление пользователями и исследованиями.
          </p>
        </div>
        <Button variant="outline" onClick={() => void refresh()} disabled={refreshing}>
          <RefreshCwIcon className={refreshing ? "size-4 animate-spin" : "size-4"} />
          Обновить
        </Button>
      </div>

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Kpi label="Исследования" value={String(stats.total)} hint={`в работе: ${stats.running}`} />
        <Kpi label="Завершено / провалено" value={`${stats.done} / ${stats.failed}`} hint="по статусам прогонов" />
        <Kpi
          label="HTTP 5xx"
          value={stats.httpTotal > 0 ? formatPercent((stats.http5xx / stats.httpTotal) * 100) : "—"}
          hint={`${stats.http5xx} из ${stats.httpTotal} запросов`}
        />
        <Kpi
          label="Средняя латентность API"
          value={formatMs(stats.apiLatency)}
          hint={stats.runLatency !== null ? `прогон: ${formatMs(stats.runLatency)}` : "без учёта SSE"}
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <UsersIcon className="size-4 shrink-0" />
            Пользователи
          </CardTitle>
          <CardDescription>Демонстрационные учётные записи и их права.</CardDescription>
        </CardHeader>
        <CardContent>
          {loading ? (
            <div className="space-y-2">
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
            </div>
          ) : (
            <ul className="divide-y">
              {users.map((user) => (
                <li key={user.subject} className="flex flex-wrap items-center gap-3 py-3">
                  <span className="font-medium">{user.subject}</span>
                  <Badge variant={user.role === "admin" ? "default" : "secondary"}>
                    {ROLE_LABEL[user.role] ?? user.role}
                  </Badge>
                  {user.read_only && (
                    <span className="text-xs text-muted-foreground">только чтение</span>
                  )}
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Исследования</CardTitle>
          <CardDescription>Всего: {runs.length}. Удаление необратимо.</CardDescription>
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
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={`Удалить исследование ${run.profession}`}
                    className="shrink-0 text-muted-foreground hover:text-destructive"
                    disabled={deletingId === run.id}
                    onClick={() => void removeRun(run)}
                  >
                    <Trash2Icon className="size-4" />
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <ActivityIcon className="size-4 shrink-0" />
            LLM-запросы
          </CardTitle>
          <CardDescription>Последние обращения к модели: статус, латентность, ошибки.</CardDescription>
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
                    <th className="py-2 pr-3 font-medium">Латентность</th>
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
                          : "—"}
                      </td>
                      <td className="py-2 pr-3">{request.agent}</td>
                      <td className="py-2 pr-3 text-muted-foreground">{request.model}</td>
                      <td className="py-2 pr-3">
                        <Badge variant={request.status === "ok" ? "secondary" : "destructive"}>
                          {request.status}
                        </Badge>
                      </td>
                      <td className="py-2 pr-3">{formatMs(request.latency_ms)}</td>
                      <td className="py-2 text-muted-foreground">{request.error_code || "—"}</td>
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
          <CardTitle className="text-base">Латентность LLM</CardTitle>
          <CardDescription>
            Последние {llmLatencies.length} запросов: динамика времени ответа модели.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-2">
          <Sparkline values={llmLatencies} className="text-primary" />
          {llmLatencyStats && (
            <div className="flex flex-wrap gap-4 text-xs text-muted-foreground">
              <span>мин: {formatMs(llmLatencyStats.min)}</span>
              <span>средняя: {formatMs(llmLatencyStats.avg)}</span>
              <span>макс: {formatMs(llmLatencyStats.max)}</span>
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
          <CardTitle className="text-base">HTTP по маршрутам</CardTitle>
          <CardDescription>Запросы, ошибки 5xx и средняя латентность.</CardDescription>
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
                    <th className="py-2 pr-3 font-medium">5xx</th>
                    <th className="py-2 font-medium">Средняя латентность</th>
                  </tr>
                </thead>
                <tbody>
                  {routeStats.map((entry) => (
                    <tr key={entry.route} className="border-b last:border-b-0">
                      <td className="py-2 pr-3 font-mono text-xs">{entry.route}</td>
                      <td className="py-2 pr-3">{entry.requests}</td>
                      <td className="py-2 pr-3">{entry.errors}</td>
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
