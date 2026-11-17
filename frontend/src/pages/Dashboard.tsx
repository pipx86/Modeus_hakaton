import { useCallback, useEffect, useState } from "react"
import { Link } from "react-router-dom"
import { toast } from "sonner"
import { AlertCircleIcon, ChevronRightIcon, FlaskConicalIcon, PlusIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { StatusBadge } from "@/components/status-badge"
import { apiClient, type Run } from "@/lib/api/client"

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

/**
 * Список исследований.
 *
 * Три состояния экрана: загрузка (скелетон), ошибка (баннер с повтором и тост),
 * пустой список (подсказка с переходом к созданию). Демо-данные при ошибке больше
 * не подставляются: пользователь должен видеть, что связь с бэкендом потеряна.
 */
export function Dashboard() {
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

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

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="space-y-1">
          <h1 className="text-3xl font-semibold tracking-tight">Исследования</h1>
          <p className="text-sm text-muted-foreground">
            {loading ? "Загрузка…" : `${runs.length} исследований, ${active} в работе`}
          </p>
        </div>
        <Button asChild>
          <Link to="/research/new">
            <PlusIcon className="size-4" />
            Новое исследование
          </Link>
        </Button>
      </div>

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
          <Button asChild>
            <Link to="/research/new">
              <PlusIcon className="size-4" />
              Новое исследование
            </Link>
          </Button>
        </div>
      ) : (
        <div className="overflow-hidden rounded-xl border">
          <ul className="divide-y">
            {runs.map((run) => (
              <li key={run.id}>
                <Link
                  to={`/research/${run.id}`}
                  className="flex items-center gap-4 px-4 py-3.5 transition-colors hover:bg-muted/60"
                >
                  <div className="min-w-0 flex-1">
                    <p className="truncate font-medium">{run.profession}</p>
                    <p className="text-xs text-muted-foreground">{formatDate(run.started_at)}</p>
                  </div>
                  <StatusBadge status={run.status} />
                  <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
                </Link>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}
