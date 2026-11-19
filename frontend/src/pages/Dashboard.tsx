/**
 * @file Dashboard.tsx
 * @description Главный экран: список исследований, KPI, графики активности и демо-прогон.
 */

import { useCallback, useEffect, useMemo, useState } from "react"
import { Link } from "react-router-dom"
import { toast } from "sonner"
import { AlertCircleIcon, ChevronRightIcon, FlaskConicalIcon, PlusIcon, Trash2Icon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Skeleton } from "@/components/ui/skeleton"
import { StatusBadge } from "@/components/status-badge"
import { Donut, StackedBars } from "@/components/charts"
import { apiClient, type Run } from "@/lib/api/client"
import { getCurrentRole } from "@/lib/auth"

const DAYS = 14

const STAGE_LABEL: Record<string, string> = {
  created: "Создано",
  interview: "Интервью",
  analysis: "Анализ",
  synthesis: "Синтез",
  review: "Проверка",
  done: "Готово",
  failed: "Ошибка",
}

// Цвета этапов согласованы со статусами: ошибка - красный, готово - зелёный,
// текущая работа - янтарный, промежуточные шаги - свои оттенки.
const STAGE_COLOR: Record<string, string> = {
  created: "bg-muted-foreground/40",
  interview: "bg-warning",
  analysis: "bg-sky-500",
  synthesis: "bg-primary",
  review: "bg-cyan-500",
  done: "bg-success",
  failed: "bg-destructive",
}

function formatDate(value?: string) {
  if (!value) return "Дата не указана"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat("ru-RU", {
    day: "numeric",
    month: "long",
    year: "numeric",
  }).format(date)
}

function formatDuration(ms: number | null): string {
  if (ms === null) return "-"
  if (ms < 60_000) return `${Math.round(ms / 1000)} с`
  if (ms < 3_600_000) return `${(ms / 60_000).toFixed(1)} мин`
  return `${(ms / 3_600_000).toFixed(1)} ч`
}

/**
 * Список исследований и обзорная аналитика.
 *
 * Три состояния экрана: загрузка (скелетон), ошибка (баннер с повтором и тост),
 * пустой список (подсказка с переходом к созданию). Демо-данные при ошибке больше
 * не подставляются: пользователь должен видеть, что связь с бэкендом потеряна.
 */
export function Dashboard() {
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [pendingDelete, setPendingDelete] = useState<Run | null>(null)
  const [deleting, setDeleting] = useState(false)
  const canCreate = getCurrentRole() !== "guest"
  const canDelete =
    getCurrentRole() === null ||
    getCurrentRole() === "admin" ||
    getCurrentRole() === "teacher" ||
    getCurrentRole() === "rop"

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const { data, error: apiError } = await apiClient.GET("/runs")
      if (apiError) {
        setError("Не удалось загрузить исследования")
        setRuns([])
        toast.error("Не удалось загрузить исследования", {
          description: "Сервер вернул ошибку. Попробуйте ещё раз.",
        })
        return
      }
      setRuns(data ?? [])
    } catch {
      setError("Сервер недоступен")
      setRuns([])
      toast.error("Сервер недоступен", {
        description: "Проверьте, что бэкенд запущен по адресу из VITE_API_URL.",
      })
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    // Данные загружаются асинхронно; состояние обновляется после ответа API.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load()
  }, [load])

  const active = runs.filter((run) => run.status === "running").length
  const demoRun = runs.find((run) => run.status === "done")

  const stats = useMemo(() => {
    const done = runs.filter((run) => run.status === "done").length
    const failed = runs.filter((run) => run.status === "failed").length
    const durations = runs
      .filter((run) => run.started_at && run.finished_at)
      .map((run) => new Date(run.finished_at as string).getTime() - new Date(run.started_at as string).getTime())
      .filter((value) => Number.isFinite(value) && value > 0)
    const average = durations.length
      ? durations.reduce((sum, value) => sum + value, 0) / durations.length
      : null
    const successRate = done + failed > 0 ? (done / (done + failed)) * 100 : null
    return { done, failed, average, successRate }
  }, [runs])

  const daily = useMemo(() => {
    const days: {
      key: string
      label: string
      done: number
      running: number
      failed: number
      other: number
    }[] = []
    for (let index = DAYS - 1; index >= 0; index -= 1) {
      const date = new Date()
      date.setHours(0, 0, 0, 0)
      date.setDate(date.getDate() - index)
      days.push({
        key: date.toISOString().slice(0, 10),
        label: `${String(date.getDate()).padStart(2, "0")}.${String(date.getMonth() + 1).padStart(2, "0")}`,
        done: 0,
        running: 0,
        failed: 0,
        other: 0,
      })
    }
    const index = new Map(days.map((day) => [day.key, day]))
    for (const run of runs) {
      if (!run.started_at) continue
      const bucket = index.get(new Date(run.started_at).toISOString().slice(0, 10))
      if (!bucket) continue
      if (run.status === "done") bucket.done += 1
      else if (run.status === "failed") bucket.failed += 1
      else if (run.status === "running") bucket.running += 1
      else bucket.other += 1
    }
    return days.map((day) => ({
      label: day.label,
      segments: [
        { value: day.done, className: "bg-emerald-500" },
        { value: day.running, className: "bg-warning" },
        { value: day.failed, className: "bg-destructive" },
        { value: day.other, className: "bg-muted-foreground/40" },
      ],
    }))
  }, [runs])

  const stages = useMemo(() => {
    const counts = new Map<string, number>()
    for (const run of runs) {
      const stage = run.stage ?? "created"
      counts.set(stage, (counts.get(stage) ?? 0) + 1)
    }
    return Array.from(counts.entries())
      .map(([stage, count]) => ({ stage, count }))
      .sort((left, right) => right.count - left.count)
  }, [runs])

  const statusSegments = [
    { label: "done", value: stats.done, className: "text-emerald-500" },
    { label: "running", value: active, className: "text-warning" },
    { label: "failed", value: stats.failed, className: "text-destructive" },
    {
      label: "other",
      value: Math.max(0, runs.length - stats.done - active - stats.failed),
      className: "text-muted-foreground/60",
    },
  ]

  const removeRun = async () => {
    if (!pendingDelete) return
    setDeleting(true)
    try {
      const { error: apiError } = await apiClient.DELETE("/runs/{id}", {
        params: { path: { id: pendingDelete.id } },
      })
      if (apiError) {
        toast.error("Не удалось удалить исследование", { description: apiError.message })
        return
      }
      setRuns((prev) => prev.filter((run) => run.id !== pendingDelete.id))
      toast.success("Исследование удалено")
      setPendingDelete(null)
    } catch {
      toast.error("Сервер недоступен", { description: "Исследование не удалено." })
    } finally {
      setDeleting(false)
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="space-y-1">
          <h1 className="text-3xl font-semibold tracking-tight">Исследования</h1>
          <p className="text-sm text-muted-foreground">
            {loading ? "Загрузка…" : `${runs.length} исследований, ${active} в работе`}
          </p>
        </div>
        <div className="flex w-full flex-wrap gap-2 sm:w-auto">
          {demoRun && (
            <Button variant="outline" asChild className="w-full sm:w-auto">
              <Link to={`/research/${demoRun.id}`}>
                <FlaskConicalIcon className="size-4" />
                Посмотреть пример
              </Link>
            </Button>
          )}
          {canCreate && (
          <Button asChild className="w-full sm:w-auto">
            <Link to="/research/new">
              <PlusIcon className="size-4" />
              Новое исследование
            </Link>
          </Button>
          )}
        </div>
      </div>

      {!loading && !error && runs.length > 0 && (
        <>
          <div className="grid grid-cols-2 gap-3 xl:grid-cols-4">
            <Card size="sm">
              <CardContent className="space-y-1">
                <p className="text-xs text-muted-foreground">Всего исследований</p>
                <p className="text-2xl font-semibold">{runs.length}</p>
              </CardContent>
            </Card>
            <Card size="sm">
              <CardContent className="space-y-1">
                <p className="text-xs text-muted-foreground">Активных</p>
                <p className="text-2xl font-semibold">{active}</p>
              </CardContent>
            </Card>
            <Card size="sm">
              <CardContent className="space-y-1">
                <p className="text-xs text-muted-foreground">Успешных</p>
                <p className="text-2xl font-semibold">
                  {stats.successRate === null ? "-" : `${Math.round(stats.successRate)}%`}
                </p>
                <p className="text-xs text-muted-foreground">
                  {stats.done} из {stats.done + stats.failed} завершённых
                </p>
              </CardContent>
            </Card>
            <Card size="sm">
              <CardContent className="space-y-1">
                <p className="text-xs text-muted-foreground">Средняя длительность</p>
                <p className="text-2xl font-semibold">{formatDuration(stats.average)}</p>
              </CardContent>
            </Card>
          </div>

          <div className="grid gap-3 lg:grid-cols-3">
            <Card className="lg:col-span-2">
              <CardHeader>
                <CardTitle className="text-base">Активность за 14 дней</CardTitle>
                <CardDescription>
                  Столбики по дням: успешные, активные, с ошибкой.
                </CardDescription>
              </CardHeader>
              <CardContent>
                <StackedBars data={daily} />
                <div className="mt-3 flex flex-wrap gap-3 text-xs text-muted-foreground">
                  <span className="flex items-center gap-1.5">
                    <span className="size-2 rounded-full bg-emerald-500" /> успешные
                  </span>
                  <span className="flex items-center gap-1.5">
                    <span className="size-2 rounded-full bg-warning" /> в работе
                  </span>
                  <span className="flex items-center gap-1.5">
                    <span className="size-2 rounded-full bg-destructive" /> с ошибкой
                  </span>
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle className="text-base">Статусы</CardTitle>
                <CardDescription>Распределение всех прогонов.</CardDescription>
              </CardHeader>
              <CardContent className="flex justify-center">
                <Donut
                  segments={statusSegments}
                  centerLabel="прогонов"
                  centerValue={String(runs.length)}
                />
              </CardContent>
            </Card>
          </div>

          <Card>
            <CardHeader>
              <CardTitle className="text-base">Этапы прогонов</CardTitle>
              <CardDescription>На каком шаге сейчас находятся исследования.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-2">
              {stages.map((item) => {
                const percent = runs.length > 0 ? (item.count / runs.length) * 100 : 0
                return (
                  <div key={item.stage} className="flex items-center gap-3 text-sm">
                    <span className="flex w-28 shrink-0 items-center gap-2 text-muted-foreground">
                      <span
                        aria-hidden
                        className={`size-2 shrink-0 rounded-full ${STAGE_COLOR[item.stage] ?? "bg-muted-foreground/40"}`}
                      />
                      {STAGE_LABEL[item.stage] ?? item.stage}
                    </span>
                    <div className="h-2 flex-1 overflow-hidden rounded-full bg-muted">
                      <div
                        className={`h-full rounded-full ${STAGE_COLOR[item.stage] ?? "bg-muted-foreground/40"}`}
                        style={{ width: `${percent}%` }}
                      />
                    </div>
                    <span className="w-8 shrink-0 text-right">{item.count}</span>
                  </div>
                )
              })}
            </CardContent>
          </Card>
        </>
      )}

      {loading ? (
        <div className="overflow-hidden rounded-xl border">
          {[0, 1, 2].map((row) => (
            <div key={row} className="flex items-center gap-4 border-b px-4 py-4 last:border-b-0">
              <Skeleton className="h-4 w-1/3" />
              <Skeleton className="ml-auto h-5 w-24 rounded-full" />
              <Skeleton className="size-4" />
            </div>
          ))}
        </div>
      ) : error ? (
        <div className="flex flex-col items-center justify-center gap-3 rounded-xl border border-destructive/50 p-10 text-center">
          <AlertCircleIcon className="size-6 text-destructive" />
          <div className="space-y-1">
            <p className="font-medium">{error}</p>
            <p className="text-sm text-muted-foreground">
              Проверьте, что бэкенд или мок-сервер запущен, и повторите запрос.
            </p>
          </div>
          <Button variant="outline" onClick={() => void load()}>
            Повторить
          </Button>
        </div>
      ) : runs.length === 0 ? (
        <div className="flex flex-col items-center justify-center gap-4 rounded-xl border border-dashed py-16 text-center">
          <div className="flex size-12 items-center justify-center rounded-full bg-muted">
            <FlaskConicalIcon className="size-6 text-muted-foreground" />
          </div>
          <div className="space-y-1">
            <p className="font-medium">Исследований пока нет</p>
            <p className="text-sm text-muted-foreground">
              Создайте первое исследование, чтобы запустить анализ профессиональной деятельности.
            </p>
          </div>
          {canCreate && (
          <Button asChild>
            <Link to="/research/new">
              <PlusIcon className="size-4" />
              Новое исследование
            </Link>
          </Button>
          )}
        </div>
      ) : (
        <div className="overflow-hidden rounded-xl border">
          <ul className="divide-y">
            {runs.map((run) => (
              <li key={run.id} className="flex items-center gap-1 pr-2">
                <Link
                  to={`/research/${run.id}`}
                  className="flex min-w-0 flex-1 items-center gap-4 px-4 py-3.5 transition-colors hover:bg-muted/60"
                >
                  <div className="min-w-0 flex-1">
                    <p className="truncate font-medium">{run.profession}</p>
                    <p className="text-xs text-muted-foreground">{formatDate(run.started_at)}</p>
                  </div>
                  <StatusBadge status={run.status} />
                  <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
                </Link>
                {canDelete && (
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={`Удалить исследование ${run.profession}`}
                  className="shrink-0 text-muted-foreground hover:text-destructive"
                  onClick={() => setPendingDelete(run)}
                >
                  <Trash2Icon className="size-4" />
                </Button>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}

      <Dialog open={pendingDelete !== null} onOpenChange={(open) => !open && setPendingDelete(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Удалить исследование?</DialogTitle>
            <DialogDescription>
              «{pendingDelete?.profession}» будет удалено вместе с деревом, источниками и чатом.
              Действие необратимо.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPendingDelete(null)}>
              Отмена
            </Button>
            <Button variant="destructive" onClick={() => void removeRun()} disabled={deleting}>
              {deleting ? "Удаляю…" : "Удалить"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
