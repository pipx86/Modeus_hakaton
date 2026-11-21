/**
 * @file activity-changes-report.tsx
 * @description Отчёт об изменениях профессиональной деятельности при повторном
 * исследовании: `GET /runs/{id}/activity-changes?against={run_id}`.
 *
 * Секция вкладки «Снимки и проверка». Методист выбирает второе исследование и
 * видит, какие функции/задачи/действия появились, исчезли или изменились и
 * какие новые материалы их подтверждают. Сравнение делает бэкенд по
 * семантическому ключу (тип + нормализованное название + путь предков),
 * поэтому added/removed не зависят от разницы id у независимых прогонов.
 *
 * Пустые состояния нейтральные: «нет второго исследования», «у исследования
 * ещё нет дерева» (409 TREE_MISSING) и «различий не найдено». Ответ 501 -
 * спокойное «раздел пока не поддерживается сервером», прочие 4xx/5xx -
 * видимое сообщение с кнопкой повтора.
 *
 * Экспорт: {@link ActivityChangesReport}.
 */
import { useEffect, useState } from "react"
import { GitCompareIcon, RefreshCwIcon } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { NODE_TYPE_LABELS } from "@/components/activity-tree-model"
import {
  ApiRequestError,
  getActivityChanges,
  listRuns,
  type ActivityChangeField,
  type ActivityChangeNode,
  type ActivityChangeReport,
  type ActivityChangeSource,
  type RunListItem,
  type SourceType,
} from "@/lib/api/client"

const CHANGE_FIELD_LABELS: Record<ActivityChangeField, string> = {
  title: "Название",
  description: "Описание",
  type: "Тип",
}

const SOURCE_TYPE_LABELS: Record<SourceType, string> = {
  vacancy: "Вакансия",
  interview: "Интервью",
  standard: "Профстандарт",
  document: "Документ",
}

/** Человекочитаемый тип узла; неизвестный тип показываем как есть. */
function nodeTypeLabel(type: string): string {
  return NODE_TYPE_LABELS[type as keyof typeof NODE_TYPE_LABELS] ?? type
}

/** Тип материала; неизвестный тип показываем как есть. */
function sourceTypeLabel(type: string): string {
  return SOURCE_TYPE_LABELS[type as SourceType] ?? type
}

/** Понятное сообщение об ошибке загрузки; 501 обрабатывается отдельно. */
function describeActivityChangesError(error: unknown): string {
  if (error instanceof ApiRequestError) {
    if (error.status === 403) {
      return "Доступ к отчёту есть только у владельца исследования и администратора."
    }
    if (error.status === 404) return "Исследование для сравнения не найдено."
    if (error.status === 400) return "Не выбрано исследование для сравнения."
    if (error.status === 0) return "Сервер недоступен: проверьте соединение."
    return error.message
  }
  if (error && typeof error === "object") {
    const message = (error as { message?: unknown }).message
    if (typeof message === "string" && message.trim()) return message
  }
  return "Не удалось загрузить отчёт об изменениях деятельности."
}

/** Список новых материалов, подтверждающих узел. */
function NewSources({ sources }: { sources: ActivityChangeSource[] }) {
  if (sources.length === 0) {
    return <p className="text-xs text-muted-foreground">Новых материалов нет.</p>
  }
  return (
    <div className="space-y-1">
      <p className="text-xs font-medium text-muted-foreground">Новые материалы:</p>
      <ul className="space-y-1">
        {sources.map((source) => (
          <li
            key={source.source_id}
            className="flex flex-wrap items-center gap-2 rounded-md border bg-background/60 px-2.5 py-1.5 text-xs"
          >
            <Badge variant="outline">{sourceTypeLabel(source.source_type)}</Badge>
            <span className="min-w-0 break-words">{source.source_title || "Источник без названия"}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}

/** Один узел отчёта: тип, название, родитель, обоснования и новые материалы. */
function ChangeNodeCard({ node }: { node: ActivityChangeNode }) {
  return (
    <li className="space-y-2 rounded-lg border bg-muted/20 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="secondary">{nodeTypeLabel(node.node_type)}</Badge>
        <span className="min-w-0 break-words text-sm font-medium">{node.node_title}</span>
        <Badge variant="outline" className="ml-auto">
          Обоснований: {node.evidence_count}
        </Badge>
      </div>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
        <span>
          Родитель:{" "}
          {node.parent_title ? (
            <span className="text-foreground">{node.parent_title}</span>
          ) : (
            "корневой узел"
          )}
        </span>
        {node.changed_fields && node.changed_fields.length > 0 && (
          <span>
            Изменённые поля:{" "}
            {node.changed_fields.map((field) => CHANGE_FIELD_LABELS[field] ?? field).join(", ")}
          </span>
        )}
      </div>
      <NewSources sources={node.new_sources ?? []} />
    </li>
  )
}

/** Группа узлов одной категории отчёта. */
function ChangeGroup({
  title,
  count,
  nodes,
}: {
  title: string
  count: number
  nodes: ActivityChangeNode[]
}) {
  if (count === 0) return null
  return (
    <section className="space-y-2" aria-label={title}>
      <h3 className="text-sm font-medium">
        {title} <span className="text-muted-foreground">({count})</span>
      </h3>
      <ul className="space-y-2">
        {nodes.map((node) => (
          <ChangeNodeCard key={`${node.node_id}-${node.node_title}`} node={node} />
        ))}
      </ul>
    </section>
  )
}

/**
 * Отчёт об изменениях деятельности между двумя исследованиями.
 *
 * @param runId Id текущего исследования.
 */
export function ActivityChangesReport({ runId }: { runId: string }) {
  const [runs, setRuns] = useState<RunListItem[]>([])
  const [runsLoading, setRunsLoading] = useState(true)
  const [runsError, setRunsError] = useState<string | null>(null)
  const [againstRunId, setAgainstRunId] = useState("")
  const [report, setReport] = useState<ActivityChangeReport | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notAvailable, setNotAvailable] = useState(false)
  const [treeMissing, setTreeMissing] = useState(false)
  const [reloadToken, setReloadToken] = useState(0)

  // Список исследований нужен для выбора противника. Текущее исследование
  // исключается: сравнивать прогон с самим собой бессмысленно.
  useEffect(() => {
    let cancelled = false
    const load = async () => {
      setRunsLoading(true)
      try {
        const items = await listRuns()
        if (cancelled) return
        const others = items.filter((run) => run.id !== runId)
        setRuns(others)
        setRunsError(null)
        setAgainstRunId((current) =>
          current && others.some((run) => run.id === current)
            ? current
            : others[0]?.id ?? "",
        )
      } catch (cause) {
        if (cancelled) return
        setRuns([])
        setAgainstRunId("")
        setRunsError(describeActivityChangesError(cause))
      } finally {
        if (!cancelled) setRunsLoading(false)
      }
    }
    // Загрузка асинхронная: состояние обновится после ответа API.
    void load()
    return () => {
      cancelled = true
    }
  }, [runId, reloadToken])

  useEffect(() => {
    if (!againstRunId) return
    let cancelled = false
    const load = async () => {
      setLoading(true)
      try {
        const data = await getActivityChanges(runId, againstRunId)
        if (cancelled) return
        setReport(data)
        setError(null)
        setNotAvailable(false)
        setTreeMissing(false)
      } catch (cause) {
        if (cancelled) return
        setReport(null)
        if (cause instanceof ApiRequestError && cause.status === 501) {
          setError(null)
          setNotAvailable(true)
          setTreeMissing(false)
        } else if (
          cause instanceof ApiRequestError &&
          cause.status === 409 &&
          cause.code === "TREE_MISSING"
        ) {
          setError(null)
          setNotAvailable(false)
          setTreeMissing(true)
        } else {
          setNotAvailable(false)
          setTreeMissing(false)
          setError(describeActivityChangesError(cause))
        }
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    // Загрузка асинхронная: состояние обновится после ответа API.
    void load()
    return () => {
      cancelled = true
    }
  }, [runId, againstRunId, reloadToken])

  const summary = report?.summary
  const totalChanges =
    (summary?.added ?? 0) + (summary?.removed ?? 0) + (summary?.changed ?? 0)
  const sourceDelta = (summary?.sources_added ?? 0) + (summary?.sources_removed ?? 0)
  const hasOpponents = runs.length > 0

  return (
    <Card className="gap-4 p-5">
      <CardHeader>
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="space-y-1">
            <CardTitle className="flex items-center gap-2">
              <GitCompareIcon className="size-4 shrink-0" />
              Изменения деятельности при повторном исследовании
            </CardTitle>
            <CardDescription>
              Сравнение функций, задач и действий двух исследований по формулировкам,
              а не по идентификаторам. Отчёт показывает появившиеся, исчезнувшие и
              изменённые элементы и новые материалы, которые их подтверждают; решение
              по-прежнему принимает методист.
            </CardDescription>
          </div>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Обновить отчёт об изменениях"
            disabled={loading || runsLoading}
            onClick={() => setReloadToken((token) => token + 1)}
          >
            <RefreshCwIcon className={`size-4 ${loading || runsLoading ? "animate-spin" : ""}`} />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {runsLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-8 w-2/3" />
            <Skeleton className="h-24 w-full" />
          </div>
        ) : runsError ? (
          <div className="space-y-3 rounded-lg border border-destructive/50 p-4 text-sm">
            <p className="text-destructive" role="alert">
              {runsError}
            </p>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setReloadToken((token) => token + 1)}
            >
              Повторить загрузку
            </Button>
          </div>
        ) : !hasOpponents ? (
          <div className="space-y-1 rounded-lg border border-dashed p-6 text-center">
            <p className="font-medium">Пока не с чем сравнивать</p>
            <p className="text-sm text-muted-foreground">
              Отчёт появится, когда в системе будет второе исследование с деревом
              деятельности.
            </p>
          </div>
        ) : (
          <>
            <div className="space-y-1">
              <label
                htmlFor="activity-changes-against"
                className="text-sm font-medium"
              >
                Сравнить с исследованием
              </label>
              <select
                id="activity-changes-against"
                aria-label="Исследование для сравнения"
                value={againstRunId}
                onChange={(event) => setAgainstRunId(event.target.value)}
                className="h-9 w-full rounded-md border bg-background px-3 text-sm"
              >
                {runs.map((run) => (
                  <option key={run.id} value={run.id}>
                    {run.profession || "Без названия профессии"} - {run.id.slice(0, 8)}
                  </option>
                ))}
              </select>
            </div>

            {loading ? (
              <div className="space-y-2">
                <Skeleton className="h-8 w-2/3" />
                <Skeleton className="h-24 w-full" />
              </div>
            ) : error ? (
              <div className="space-y-3 rounded-lg border border-destructive/50 p-4 text-sm">
                <p className="text-destructive" role="alert">
                  {error}
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setReloadToken((token) => token + 1)}
                >
                  Повторить загрузку отчёта
                </Button>
              </div>
            ) : notAvailable ? (
              <div className="space-y-1 rounded-lg border border-dashed p-6 text-center">
                <p className="font-medium">Отчёт пока недоступен</p>
                <p className="text-sm text-muted-foreground">
                  Раздел пока не поддерживается сервером.
                </p>
              </div>
            ) : treeMissing ? (
              <div className="space-y-1 rounded-lg border border-dashed p-6 text-center">
                <p className="font-medium">Сравнивать пока нечего</p>
                <p className="text-sm text-muted-foreground">
                  У одного из исследований ещё нет дерева деятельности. Отчёт появится
                  после построения дерева.
                </p>
              </div>
            ) : !report || !summary ? null : (
              <>
                {!report.same_profession && (
                  <p className="rounded-lg border bg-muted/30 p-3 text-sm text-muted-foreground">
                    Профессии исследований различаются. Отчёт построен, но часть
                    изменений может объясняться сменой профессии.
                  </p>
                )}
                <div className="flex flex-wrap items-center gap-2" aria-label="Счётчики изменений">
                  <Badge variant="outline" className="bg-success/15 text-success">
                    Появилось: {summary.added}
                  </Badge>
                  <Badge variant="outline" className="bg-destructive/15 text-destructive">
                    Исчезло: {summary.removed}
                  </Badge>
                  <Badge variant="outline" className="bg-warning/15 text-warning">
                    Изменилось: {summary.changed}
                  </Badge>
                  <Badge variant="outline">Новых материалов: {summary.sources_added}</Badge>
                  <Badge variant="outline">
                    Материалов больше не используется: {summary.sources_removed}
                  </Badge>
                </div>

                {totalChanges === 0 ? (
                  <div className="space-y-1 rounded-lg border border-dashed p-6 text-center">
                    <p className="font-medium">
                      {sourceDelta > 0 ? "Состав деятельности не изменился" : "Различий не найдено"}
                    </p>
                    <p className="text-sm text-muted-foreground">
                      {sourceDelta > 0
                        ? `Но обновились подтверждающие материалы: новых - ${
                            summary.sources_added
                          }, больше не используется - ${summary.sources_removed}.`
                        : "Функции, задачи и действия обоих исследований совпадают по типу, названию и пути в дереве."}
                    </p>
                  </div>
                ) : (
                  <div className="space-y-4">
                    <ChangeGroup title="Появилось" count={summary.added} nodes={report.added} />
                    <ChangeGroup title="Исчезло" count={summary.removed} nodes={report.removed} />
                    <ChangeGroup title="Изменилось" count={summary.changed} nodes={report.changed} />
                  </div>
                )}
              </>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}
