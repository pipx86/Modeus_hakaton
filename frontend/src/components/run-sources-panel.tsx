/**
 * @file run-sources-panel.tsx
 * @description Блок «Состав использованных материалов» вкладки «Источники»:
 * материалы, реально привязанные к исследованию (`GET /runs/{id}/sources`).
 *
 * Отличается от общего списка `GET /sources`: здесь только то, что попало в
 * конкретный прогон, с числом обоснований на каждый материал. Клик по материалу
 * раскрывает его обоснования: список evidence существующего дерева
 * (`GET /runs/{id}/tree`, отфильтрованный по `source_id`), а клик по обоснованию
 * открывает диалог с исходной цитатой и контекстом ответа
 * (`GET /runs/{id}/evidence/{evidence_id}/context`). Новых API не вводим.
 *
 * Ошибки 4xx/5xx видны сообщением с кнопкой повтора; 501 - нейтральное пустое
 * состояние: раздел ещё не включён на сервере, а не «материалов нет».
 * Формы ручного согласования источников здесь нет.
 *
 * Экспорт: {@link RunSourcesPanel}.
 */
import { useCallback, useEffect, useMemo, useState } from "react"
import {
  ChevronDownIcon,
  ExternalLinkIcon,
  FileTextIcon,
  Loader2Icon,
  RefreshCwIcon,
} from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Skeleton } from "@/components/ui/skeleton"
import {
  ApiRequestError,
  apiClient,
  listRunSources,
  type ActivityNode,
  type Evidence,
  type EvidenceContext,
  type EvidenceStatus,
  type RunSource,
  type SourceType,
} from "@/lib/api/client"
import { safeHttpUrl } from "@/lib/security"

/** Человекочитаемые типы материалов; неизвестный тип показываем как есть. */
const SOURCE_TYPE_LABELS: Record<SourceType, string> = {
  interview: "Интервью",
  standard: "Профстандарт",
  vacancy: "Вакансия",
  document: "Документ",
}

const EVIDENCE_STATUS_LABELS: Record<EvidenceStatus, string> = {
  pending: "На проверке",
  approved: "Подтверждено",
  rejected: "Отклонено",
}

const EVIDENCE_STATUS_CLASS: Record<EvidenceStatus, string> = {
  pending: "bg-muted text-muted-foreground",
  approved: "bg-success/15 text-success",
  rejected: "bg-destructive/15 text-destructive",
}

/** Обоснование вместе с узлом, к которому оно привязано в дереве. */
interface SourceEvidence {
  evidence: Evidence
  nodeId: string
  nodeTitle: string
}

/** Открытое обоснование для диалога контекста. */
interface EvidenceDetail {
  evidence: Evidence
  nodeId: string
  nodeTitle: string
}

/** Форматирует дату ISO в русскую длинную форму; битое значение возвращает как есть. */
function formatDate(value?: string | null): string {
  if (!value) return "Дата не указана"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat("ru-RU", {
    day: "numeric",
    month: "long",
    year: "numeric",
  }).format(date)
}

/** Человекочитаемый тип материала. */
function sourceTypeLabel(type: SourceType | string): string {
  return SOURCE_TYPE_LABELS[type as SourceType] ?? type
}

/** Понятное сообщение об ошибке загрузки; 501 обрабатывается отдельно. */
function describeRequestError(error: unknown): string {
  if (error instanceof ApiRequestError) {
    if (error.status === 403) {
      return "Недостаточно прав для просмотра материалов исследования."
    }
    if (error.status === 404) return "Исследование или материал не найдены."
    if (error.status === 0) return "Сервер недоступен: проверьте соединение."
    return error.message
  }
  if (error && typeof error === "object") {
    const message = (error as { message?: unknown }).message
    if (typeof message === "string" && message.trim()) return message
  }
  return "Не удалось загрузить материалы исследования."
}

/**
 * Блок «Состав использованных материалов».
 *
 * @param runId Id исследования.
 * @param onFocusNode Переход к узлу на вкладке «Дерево»; без колбэка кнопка
 *   перехода в диалоге обоснования не показывается.
 */
export function RunSourcesPanel({
  runId,
  onFocusNode,
}: {
  runId: string
  onFocusNode?: (nodeId: string) => void
}) {
  const [sources, setSources] = useState<RunSource[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notAvailable, setNotAvailable] = useState(false)
  const [reloadToken, setReloadToken] = useState(0)

  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [nodes, setNodes] = useState<ActivityNode[] | null>(null)
  const [nodesLoading, setNodesLoading] = useState(false)
  const [nodesError, setNodesError] = useState<string | null>(null)

  const [detail, setDetail] = useState<EvidenceDetail | null>(null)
  const [context, setContext] = useState<EvidenceContext | null>(null)
  const [contextLoading, setContextLoading] = useState(false)
  const [contextError, setContextError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      setLoading(true)
      try {
        const data = await listRunSources(runId)
        if (cancelled) return
        setSources(data)
        setError(null)
        setNotAvailable(false)
      } catch (cause) {
        if (cancelled) return
        setSources([])
        if (cause instanceof ApiRequestError && cause.status === 501) {
          setError(null)
          setNotAvailable(true)
        } else {
          setNotAvailable(false)
          setError(describeRequestError(cause))
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
  }, [runId, reloadToken])

  const loadNodes = useCallback(async () => {
    setNodesLoading(true)
    setNodesError(null)
    try {
      const { data, error: requestError } = await apiClient.GET("/runs/{id}/tree", {
        params: { path: { id: runId } },
      })
      if (requestError) {
        setNodesError(describeRequestError(requestError))
        return
      }
      const next = Array.isArray(data) ? data : (data?.nodes ?? [])
      setNodes(next)
    } catch (cause) {
      setNodesError(describeRequestError(cause))
    } finally {
      setNodesLoading(false)
    }
  }, [runId])

  /**
   * Индекс «материал → обоснования» строится из существующего дерева: отдельного
   * run-scoped эндпоинта для evidence нет, а глобальный `/evidences` смешал бы прогоны.
   */
  const evidenceBySource = useMemo(() => {
    const map = new Map<string, SourceEvidence[]>()
    for (const node of nodes ?? []) {
      for (const evidence of node.evidence ?? []) {
        const list = map.get(evidence.source_id) ?? []
        list.push({ evidence, nodeId: node.id, nodeTitle: node.title })
        map.set(evidence.source_id, list)
      }
    }
    return map
  }, [nodes])

  const toggleSource = (sourceId: string) => {
    if (expandedId === sourceId) {
      setExpandedId(null)
      return
    }
    setExpandedId(sourceId)
    if (nodes === null && !nodesLoading) void loadNodes()
  }

  const openEvidence = async (entry: SourceEvidence) => {
    setDetail(entry)
    setContext(null)
    setContextError(null)
    setContextLoading(true)
    try {
      const { data, error: requestError } = await apiClient.GET(
        "/runs/{id}/evidence/{evidence_id}/context",
        { params: { path: { id: runId, evidence_id: entry.evidence.id } } },
      )
      if (requestError) {
        setContextError(describeRequestError(requestError))
        return
      }
      setContext(data ?? null)
    } catch (cause) {
      setContextError(describeRequestError(cause))
    } finally {
      setContextLoading(false)
    }
  }

  const closeDetail = () => {
    setDetail(null)
    setContext(null)
    setContextError(null)
    setContextLoading(false)
  }

  const matchedMessage = context?.match
    ? context.messages?.find((message) => message.id === context.match?.message_id)
    : undefined

  return (
    <Card className="gap-4 p-5">
      <CardHeader>
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="space-y-1">
            <CardTitle className="flex items-center gap-2">
              <FileTextIcon className="size-4 shrink-0" />
              Состав использованных материалов
            </CardTitle>
            <CardDescription>
              Материалы, привязанные к этому исследованию, и число обоснований, которые на
              них ссылаются. Клик по материалу показывает его обоснования.
            </CardDescription>
          </div>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Обновить материалы исследования"
            disabled={loading}
            onClick={() => setReloadToken((token) => token + 1)}
          >
            <RefreshCwIcon className="size-4" />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {loading ? (
          <div className="space-y-2">
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
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
              Повторить загрузку материалов
            </Button>
          </div>
        ) : sources.length === 0 ? (
          <div className="space-y-1 rounded-lg border border-dashed p-6 text-center">
            <p className="font-medium">Источники ещё не привязаны</p>
            <p className="text-sm text-muted-foreground">
              Когда к исследованию привяжут интервью, профстандарт, вакансию или документ,
              они появятся здесь вместе с числом обоснований.
            </p>
            {notAvailable && (
              <p className="text-sm text-muted-foreground">
                Раздел пока не поддерживается сервером.
              </p>
            )}
          </div>
        ) : (
          <ul className="divide-y overflow-hidden rounded-lg border">
            {sources.map((source) => {
              const expanded = expandedId === source.id
              const sourceEvidence = evidenceBySource.get(source.id) ?? []
              const declaredCount =
                typeof source.evidence_count === "number" ? source.evidence_count : null
              const count = nodes !== null ? sourceEvidence.length : (declaredCount ?? 0)
              const url = safeHttpUrl(source.source_url)
              return (
                <li key={source.id}>
                  <div className="flex flex-wrap items-center gap-2 px-3 py-2.5">
                    <button
                      type="button"
                      onClick={() => toggleSource(source.id)}
                      aria-expanded={expanded}
                      aria-label={
                        expanded
                          ? `Скрыть обоснования: ${source.title}`
                          : `Показать обоснования: ${source.title}`
                      }
                      className="flex min-w-0 flex-1 items-start gap-2 text-left"
                    >
                      <ChevronDownIcon
                        className={`mt-0.5 size-4 shrink-0 text-muted-foreground transition-transform ${expanded ? "rotate-180" : ""}`}
                      />
                      <span className="min-w-0 space-y-0.5">
                        <span className="block break-words text-sm font-medium">{source.title}</span>
                        <span className="block text-xs text-muted-foreground">
                          {sourceTypeLabel(source.type)} · {formatDate(source.created_at)}
                        </span>
                      </span>
                    </button>
                    <Badge variant="outline" className="shrink-0">
                      Обоснований: {count}
                    </Badge>
                    {url ? (
                      <a
                        href={url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="inline-flex shrink-0 items-center gap-1 text-xs text-primary underline underline-offset-4"
                      >
                        Открыть материал
                        <ExternalLinkIcon className="size-3" />
                      </a>
                    ) : null}
                  </div>
                  {expanded && (
                    <div className="space-y-2 border-t bg-muted/20 px-3 py-2.5">
                      {nodesLoading ? (
                        <p className="flex items-center gap-2 text-xs text-muted-foreground">
                          <Loader2Icon className="size-3.5 animate-spin" />
                          Загрузка обоснований…
                        </p>
                      ) : nodesError ? (
                        <div className="space-y-2">
                          <p className="text-sm text-destructive" role="alert">
                            {nodesError}
                          </p>
                          <Button variant="outline" size="sm" onClick={() => void loadNodes()}>
                            Повторить загрузку обоснований
                          </Button>
                        </div>
                      ) : sourceEvidence.length === 0 ? (
                        <p className="text-xs text-muted-foreground">
                          На этот материал пока нет обоснований.
                        </p>
                      ) : (
                        <ul className="space-y-1.5">
                          {sourceEvidence.map((entry) => {
                            const status = entry.evidence.status ?? "pending"
                            return (
                              <li key={entry.evidence.id}>
                                <button
                                  type="button"
                                  onClick={() => void openEvidence(entry)}
                                  aria-label={`Открыть обоснование: ${entry.evidence.quote}`}
                                  className="flex w-full flex-wrap items-center gap-x-2 gap-y-1 rounded-lg border bg-background/60 px-2.5 py-2 text-left text-sm transition-colors hover:border-primary/40 hover:bg-muted/40"
                                >
                                  <span className="min-w-0 flex-1 whitespace-pre-wrap break-words text-muted-foreground">
                                    «{entry.evidence.quote}»
                                  </span>
                                  <span className="shrink-0 text-xs text-muted-foreground">
                                    {entry.nodeTitle}
                                  </span>
                                  <span
                                    className={`shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium ${EVIDENCE_STATUS_CLASS[status]}`}
                                  >
                                    {EVIDENCE_STATUS_LABELS[status] ?? status}
                                  </span>
                                </button>
                              </li>
                            )
                          })}
                        </ul>
                      )}
                    </div>
                  )}
                </li>
              )
            })}
          </ul>
        )}
      </CardContent>

      <Dialog
        open={detail !== null}
        onOpenChange={(open) => {
          if (!open) closeDetail()
        }}
      >
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>Обоснование материала</DialogTitle>
            <DialogDescription>
              {detail?.nodeTitle ?? "Узел дерева"}
              
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <blockquote className="rounded-lg border bg-muted/30 p-3 text-sm">
              «{detail?.evidence.quote ?? ""}»
            </blockquote>
            <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
              <span
                className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${
                  EVIDENCE_STATUS_CLASS[detail?.evidence.status ?? "pending"]
                }`}
              >
                {EVIDENCE_STATUS_LABELS[detail?.evidence.status ?? "pending"] ??
                  detail?.evidence.status}
              </span>
            </div>
            {contextLoading ? (
              <p className="flex items-center gap-2 text-xs text-muted-foreground">
                <Loader2Icon className="size-3.5 animate-spin" />
                Загрузка контекста ответа…
              </p>
            ) : contextError ? (
              <p className="text-sm text-destructive" role="alert">
                {contextError}
              </p>
            ) : context?.match ? (
              <div className="space-y-1 rounded-lg border bg-muted/20 p-3 text-sm">
                <p className="text-xs font-medium text-muted-foreground">Ответ, откуда взята цитата</p>
                <p className="whitespace-pre-wrap break-words">
                  {matchedMessage?.content ?? context.match.quote}
                </p>
                {context.note && (
                  <p className="text-xs text-muted-foreground">{context.note}</p>
                )}
              </div>
            ) : context ? (
              <p className="text-sm text-muted-foreground">
                {context.note || "Контекст ответа не найден, обоснование доступно только цитатой."}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                const nodeId = detail?.nodeId
                closeDetail()
                if (nodeId) onFocusNode?.(nodeId)
              }}
              disabled={!onFocusNode || !detail?.nodeId}
            >
              Показать в дереве
            </Button>
            <Button onClick={closeDetail}>Закрыть</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  )
}
