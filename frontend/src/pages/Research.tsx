/**
 * @file Research.tsx
 * @description Страница одного исследования (прогона): шапка со статусом и автообновлением,
 * вкладки «Источники» и «Дерево» и «Чат». Владеет загрузкой данных, поллингом жизненного цикла,
 * подтверждением цитат (optimistic + rollback) и диалогами редактирования узла и
 * прикрепления обоснования. Интерактивное дерево (поиск, фильтры по типу и обоснованиям,
 * контролируемое раскрытие, ленивый рендер свёрнутых веток, диалог обоснований по клику)
 * вынесено в {@link ActivityTree}; чат-интервью -
 * в ChatTab;  страница лишь передаёт ему плоский список узлов и
 * колбэки мутаций, а диалоги монтирует рядом с деревом, поскольку их пропсы живут в
 * области видимости {@link TreeTab}.
 *
 * Экспорт: {@link Research} - компонент роута `/research/:id`.
 */
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { ChatTab } from "@/components/ChatTab"
import { useCallback, useEffect, useMemo, useState } from "react"
import { useParams, useSearchParams } from "react-router-dom"
import { toast } from "sonner"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { StatusBadge } from "@/components/status-badge"
import { ActivityTree } from "@/components/activity-tree"
import { SnapshotReviewTab } from "@/components/snapshot-review"
import { ConflictsTab } from "@/components/conflicts-tab"
import { CoverageTab } from "@/components/coverage-tab"
import { ZunSuggestionsDialog } from "@/components/zun-suggestions-dialog"
import { RunStageStepper } from "@/components/run-stage"
import { useRunPolling } from "@/hooks/use-run-polling"
import { NODE_TYPE_LABELS, type TreeNode } from "@/components/activity-tree-model"
import {
  apiBaseUrl,
  apiClient,
  type ActivityNode,
  type Evidence,
  type Insights,
  type NodeType,
  type RunCost,
  type Source,
  type SourceType,
} from "@/lib/api/client"
import { getCurrentRole, getValidToken } from "@/lib/auth"
import { stripControlChars } from "@/lib/security"
import { downloadText } from "@/lib/export-tree"
import { buildSourceIndex } from "@/lib/provenance"
import {
  CheckIcon,
  DownloadIcon,
  EyeIcon,
  PlusIcon,
  FileTextIcon,
  Loader2Icon,
  NetworkIcon,
  PrinterIcon,
  Share2Icon,
  SparklesIcon,
  PencilIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react"

const SOURCE_TYPE_LABELS: Record<SourceType, string> = {
  vacancy: "Вакансия",
  interview: "Интервью",
  standard: "Профстандарт",
  document: "Документ",
}

const EVIDENCE_STATUS_LABELS: Record<string, string> = {
  pending: "На проверке",
  approved: "Подтверждено",
  rejected: "Отклонено",
}

const EVIDENCE_STATUS_CLASS: Record<string, string> = {
  pending: "bg-muted text-muted-foreground",
  approved: "bg-success/15 text-success",
  rejected: "bg-destructive/15 text-destructive",
}

function errorMessage(error: unknown): string | undefined {
  if (!error || typeof error !== "object") return undefined
  const message = (error as { message?: unknown }).message
  return typeof message === "string" ? message : undefined
}

function confidenceClass(value: number) {
  if (value >= 0.8) return "bg-success/15 text-success"
  if (value >= 0.5) return "bg-warning/15 text-warning"
  return "bg-destructive/15 text-destructive"
}

const TAB_TRIGGER_CLASS =
  "h-full items-center justify-center py-0 leading-none data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm dark:data-[state=active]:bg-input/30"
const ROOT_PARENT = "__root__"
/**
 * Форматирует дату ISO в русскую длинную форму («6 ноября 2026»). Возвращает подсказку при
 * отсутствии значения и оригинал строки, если дату не удалось разобрать, - чтобы битые
 * данные не роняли рендер.
 */
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
 * Переиспользуемое пустое/ошибочное состояние в рамке: иконка, заголовок, описание.
 * Применяется для «источников нет», «дерево недоступно» и «дерево ещё не построено» -
 * три разных смысла одного визуального паттерна, поэтому вынесены в один компонент.
 */
function EmptyState({
  icon: Icon,
  title,
  description,
}: {
  icon: typeof NetworkIcon
  title: string
  description: string
}) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 py-16 text-center">
      <div className="flex size-12 items-center justify-center rounded-full bg-muted">
        <Icon className="size-6 text-muted-foreground" />
      </div>
      <div className="space-y-1">
        <p className="font-medium">{title}</p>
        <p className="text-sm text-muted-foreground">{description}</p>
      </div>
    </div>
  )
}

/**
 * Вкладка «Источники»: форма добавления (тип + название + содержимое), список с датами и
 * типом, раскрытие цитат источника по кнопке‑кавычке, удаление с серверной защитой
 * (источник с подтверждениями не удаляется - бэкенд вернёт ошибку, показываем тост).
 *
 * Загрузка и мутации обёрнуты в try/catch раздельно: сетевой сбой (`catch`) и ошибка
 * валидации (`error`) - разные ветви с разными текстами, их смешение раньше давало
 * «непонятную» ошибку вместо внятного сообщения. Раскрытие цитат ленивое: запрос
 * `GET /evidences?source_id` уходит только когда источник раскрыт, лиших вызовов нет.
 */
function SourcesTab({
  runId,
  canAttach,
  readOnly,
}: {
  runId: string
  canAttach: boolean
  readOnly: boolean
}) {
  const [sources, setSources] = useState<Source[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [reloadToken, setReloadToken] = useState(0)
  const [attachOpen, setAttachOpen] = useState(false)
  const [attachType, setAttachType] = useState<"standard" | "vacancy">("standard")
  const [attachTitle, setAttachTitle] = useState("")
  const [attachContent, setAttachContent] = useState("")
  const [attachUrl, setAttachUrl] = useState("")
  const [attachSaving, setAttachSaving] = useState(false)
  const [quotesSourceId, setQuotesSourceId] = useState<string | null>(null)
  const [quotes, setQuotes] = useState<Evidence[]>([])
  const [quotesLoading, setQuotesLoading] = useState(false)
  const [editingQuote, setEditingQuote] = useState<Evidence | null>(null)
  const [quoteDraft, setQuoteDraft] = useState("")
  const [quoteSaving, setQuoteSaving] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<Source | null>(null)
  const [preview, setPreview] = useState<Source | null>(null)
  const quoteDirty = editingQuote !== null && quoteDraft !== editingQuote.quote
  const [quoteFilter, setQuoteFilter] = useState<"all" | "pending" | "approved" | "rejected">("all")

  const visibleQuotes =
    quoteFilter === "all"
      ? quotes
      : quotes.filter((quote) => (quote.status ?? "pending") === quoteFilter)
  const pendingQuotes = quotes.filter((quote) => (quote.status ?? "pending") === "pending")
  const bulkSetStatus = async (action: "approve" | "reject") => {
    if (pendingQuotes.length === 0) return
    if (action === "reject") {
      for (const quote of pendingQuotes) {
        await setQuoteStatus(quote.id, "reject")
      }
      return
    }
    const ids = pendingQuotes.map((quote) => quote.id)
    const { data, error } = await apiClient.POST("/evidences/bulk-approve", {
      body: { ids },
    })
    if (error) {
      toast.error("Не удалось подтвердить цитаты", { description: error.message })
      return
    }
    const approvedIds = new Set(ids)
    setQuotes((prev) =>
      prev.map((quote) => (approvedIds.has(quote.id) ? { ...quote, status: "approved" } : quote)),
    )
    toast.success(`Подтверждено цитат: ${data?.approved ?? ids.length}`)
  }

  useEffect(() => {
    const load = async () => {
      try {
        const { data } = await apiClient.GET("/sources")
        if (data) setSources(data)
        setLoadError(null)
      } catch {
        setLoadError("Список источников недоступен: сервер не отвечает.")
      } finally {
        setLoading(false)
      }
    }
    void load()
  }, [reloadToken])

  useEffect(() => {
    if (!quotesSourceId) return
    let cancelled = false
    const loadQuotes = async () => {
      setQuotesLoading(true)
      const { data } = await apiClient.GET("/evidences", {
        params: { query: { source_id: quotesSourceId } },
      })
      if (cancelled) return
      setQuotes(data ?? [])
      setQuotesLoading(false)
    }
    void loadQuotes()
    return () => {
      cancelled = true
    }
  }, [quotesSourceId])

  const toggleQuotes = (sourceId: string) => {
    if (quotesSourceId === sourceId) {
      setQuotesSourceId(null)
      setQuotes([])
      setQuotesLoading(false)
      return
    }
    setQuotes([])
    setQuotesLoading(true)
    setQuotesSourceId(sourceId)
  }

  const openQuoteEditor = (quote: Evidence) => {
    setEditingQuote(quote)
    setQuoteDraft(quote.quote)
  }

  const saveQuote = async () => {
    if (!editingQuote || !quoteDraft.trim() || quoteSaving) return
    setQuoteSaving(true)
    const { data, error } = await apiClient.PATCH("/evidences/{id}", {
      params: { path: { id: editingQuote.id } },
      body: { quote: quoteDraft.trim() },
    })
    setQuoteSaving(false)
    if (error) {
      toast.error("Не удалось обновить цитату", { description: errorMessage(error) })
      return
    }
    setQuotes((prev) =>
      prev.map((item) => {
        if (item.id !== editingQuote.id) return item
        const updated: Evidence = data ?? { ...item, quote: quoteDraft.trim(), status: "pending" }
        return updated
      }),
    )
    setEditingQuote(null)
    toast.success("Цитата обновлена")
  }

  const setQuoteStatus = async (evidenceId: string, action: "approve" | "reject") => {
    const result =
      action === "approve"
        ? await apiClient.POST("/evidences/{id}/approve", {
            params: { path: { id: evidenceId } },
          })
        : await apiClient.POST("/evidences/{id}/reject", {
            params: { path: { id: evidenceId } },
          })
    if (result.error) {
      toast.error("Не удалось изменить статус цитаты", { description: errorMessage(result.error) })
      return
    }
    setQuotes((prev) =>
      prev.map((item) =>
        item.id === evidenceId
          ? { ...item, status: action === "approve" ? "approved" : "rejected" }
          : item,
      ),
    )
    toast.success(action === "approve" ? "Цитата подтверждена" : "Цитата отклонена")
  }

  const remove = async (sourceId: string) => {
    let error: { message?: string } | undefined
    try {
      const response = await apiClient.DELETE("/sources/{id}", {
        params: { path: { id: sourceId } },
      })
      error = response.error
    } catch {
      toast.error("Сервер недоступен", { description: "Источник не удалён." })
      return
    }
    if (error) {
      toast.error("Не удалось удалить источник", { description: errorMessage(error) })
      return
    }
    setSources((prev) => prev.filter((source) => source.id !== sourceId))
    if (quotesSourceId === sourceId) {
      setQuotesSourceId(null)
      setQuotes([])
      setQuotesLoading(false)
    }
    toast.success("Источник удалён")
  }

  const attachSource = async () => {
    if (!attachTitle.trim() || !attachContent.trim()) {
      toast.error("Заполните название и текст источника")
      return
    }
    setAttachSaving(true)
    try {
      const { error } = await apiClient.POST("/runs/{id}/sources", {
        params: { path: { id: runId } },
        body: {
          type: attachType,
          title: stripControlChars(attachTitle).trim(),
          content: stripControlChars(attachContent).trim(),
          source_url: attachUrl.trim() || undefined,
        },
      })
      if (error) {
        toast.error("Не удалось привязать источник", { description: errorMessage(error) })
        return
      }
      toast.success("Источник привязан к прогону")
      setAttachOpen(false)
      setAttachTitle("")
      setAttachContent("")
      setAttachUrl("")
      setReloadToken((token) => token + 1)
    } catch {
      toast.error("Сервер недоступен", { description: "Источник не привязан." })
    } finally {
      setAttachSaving(false)
    }
  }

  return (
    <div className="space-y-6">
      {canAttach && (
        <div className="flex justify-end">
          <Button
            variant="outline"
            size="sm"
            className="h-7 gap-1 px-2.5 text-xs"
            onClick={() => setAttachOpen(true)}
          >
            <PlusIcon className="size-3.5" />
            Добавить стандарт или вакансию
          </Button>
        </div>
      )}
      {loadError ? (
        <div className="rounded-xl border border-destructive/50 p-6 text-center text-sm text-destructive">
          {loadError}
        </div>
      ) : loading ? (
        <div className="overflow-hidden rounded-xl border">
          {[0, 1, 2].map((row) => (
            <div key={row} className="flex items-center gap-4 border-b px-4 py-4 last:border-b-0">
              <Skeleton className="h-4 w-1/3" />
              <Skeleton className="ml-auto h-5 w-20 rounded-full" />
            </div>
          ))}
        </div>
      ) : sources.length === 0 ? (
        <div className="flex flex-col rounded-xl border">
          <EmptyState
            icon={FileTextIcon}
            title="Источников пока нет"
            description="Добавьте первый документ, чтобы агенты могли опереться на факты."
          />
        </div>
      ) : (
        <ul className="divide-y overflow-hidden rounded-xl border">
          {sources.map((source) => {
            const expanded = quotesSourceId === source.id
            return (
              <li key={source.id}>
                <div className="flex items-center gap-3 px-4 py-3">
                  <FileTextIcon className="size-4 shrink-0 text-muted-foreground" />
                  <button
                    type="button"
                    onClick={() => toggleQuotes(source.id)}
                    aria-label={expanded ? "Скрыть цитаты" : "Показать цитаты"}
                    aria-expanded={expanded}
                    className="min-w-0 flex-1 text-left"
                  >
                    <p className="truncate text-sm font-medium">{source.title}</p>
                    <p className="text-xs text-muted-foreground">{formatDate(source.created_at)}</p>
                  </button>
                  <span className="shrink-0 rounded-full border px-2 py-0.5 text-xs text-muted-foreground">
                    {SOURCE_TYPE_LABELS[source.type]}
                  </span>
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => setPreview(source)}
                    aria-label="Просмотреть источник"
                  >
                    <EyeIcon className="size-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => setDeleteTarget(source)}
                    aria-label="Удалить источник"
                    disabled={readOnly}
                  >
                    <Trash2Icon className="size-4" />
                  </Button>
                </div>
                {expanded && (
                  <div className="space-y-1.5 bg-muted/30 px-4 pb-4 pt-1">
                    <div className="flex flex-wrap items-center gap-1.5 pb-1">
                      {(
                        [
                          ["all", "Все"],
                          ["pending", "На проверке"],
                          ["approved", "Подтверждены"],
                          ["rejected", "Отклонены"],
                        ] as const
                      ).map(([value, label]) => (
                        <Button
                          key={value}
                          size="sm"
                          variant={quoteFilter === value ? "secondary" : "ghost"}
                          className="h-6 px-2 text-xs"
                          aria-pressed={quoteFilter === value}
                          onClick={() => setQuoteFilter(value)}
                        >
                          {label}
                        </Button>
                      ))}
                      <span className="mx-auto" />
                      <Button
                        size="sm"
                        variant="outline"
                        className="h-6 px-2 text-xs"
                        disabled={pendingQuotes.length === 0 || readOnly}
                        onClick={() => void bulkSetStatus("approve")}
                      >
                        Подтвердить все
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        className="h-6 px-2 text-xs"
                        disabled={pendingQuotes.length === 0 || readOnly}
                        onClick={() => void bulkSetStatus("reject")}
                      >
                        Отклонить все
                      </Button>
                    </div>
                    {quotesLoading ? (
                      <p className="text-xs text-muted-foreground">Загрузка цитат…</p>
                    ) : quotes.length === 0 ? (
                      <p className="text-xs text-muted-foreground">
                        Из этого источника пока не взято ни одной цитаты.
                      </p>
                    ) : visibleQuotes.length === 0 ? (
                      <p className="text-xs text-muted-foreground">
                        Нет цитат с выбранным статусом.
                      </p>
                    ) : (
                      visibleQuotes.map((quote) => {
                        const confidence = (quote as Evidence & { confidence?: number }).confidence
                        return (
                        <div
                          key={quote.id}
                          className="flex flex-wrap items-center gap-2 rounded-lg border bg-background/40 px-3 py-2 text-sm"
                        >
                          <span className="min-w-0 flex-1 text-muted-foreground">{quote.quote}</span>
                          {typeof confidence === "number" && (
                            <span
                              className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${confidenceClass(confidence)}`}
                            >
                              {Math.round(confidence * 100)}%
                            </span>
                          )}
                          <span
                            className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${EVIDENCE_STATUS_CLASS[quote.status ?? "pending"]}`}
                          >
                            {EVIDENCE_STATUS_LABELS[quote.status ?? "pending"]}
                          </span>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-7 shrink-0"
                            aria-label="Редактировать цитату"
                            onClick={() => openQuoteEditor(quote)}
                            disabled={readOnly}
                          >
                            <PencilIcon className="size-3.5" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-7 shrink-0"
                            aria-label="Подтвердить цитату из источника"
                            onClick={() => void setQuoteStatus(quote.id, "approve")}
                            disabled={readOnly}
                          >
                            <CheckIcon className="size-3.5" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-7 shrink-0"
                            aria-label="Отклонить цитату из источника"
                            onClick={() => void setQuoteStatus(quote.id, "reject")}
                            disabled={readOnly}
                          >
                            <XIcon className="size-3.5" />
                          </Button>
                        </div>
                        )
                      })
                    )}
                  </div>
                )}
              </li>
            )
          })}
        </ul>
      )}

      <Dialog
        open={editingQuote !== null}
        onOpenChange={(open) => {
          if (!open) setEditingQuote(null)
        }}
      >
        <DialogContent
          className="sm:max-w-md"
          onEscapeKeyDown={(event) => {
            if (quoteDirty) event.preventDefault()
          }}
          onPointerDownOutside={(event) => {
            if (quoteDirty) event.preventDefault()
          }}
        >
          <DialogHeader>
            <DialogTitle>Редактировать цитату</DialogTitle>
            <DialogDescription>
              После изменения текста цитата снова уходит на проверку.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="quote-edit">Цитата</Label>
            <Textarea
              id="quote-edit"
              value={quoteDraft}
              onChange={(event) => setQuoteDraft(event.target.value)}
              rows={4}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditingQuote(null)}>
              Отмена
            </Button>
            <Button onClick={() => void saveQuote()} disabled={!quoteDraft.trim() || quoteSaving}>
              {quoteSaving && <Loader2Icon className="size-4 animate-spin" />}
              Сохранить
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog
        open={preview !== null}
        onOpenChange={(open) => {
          if (!open) setPreview(null)
        }}
      >
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{preview?.title ?? ""}</DialogTitle>
            <DialogDescription>
              {preview
                ? `${SOURCE_TYPE_LABELS[preview.type]} · ${formatDate(preview.created_at)}`
                : ""}
            </DialogDescription>
          </DialogHeader>
          <div className="max-h-72 overflow-y-auto whitespace-pre-wrap rounded-lg border bg-muted/30 p-3 text-sm">
            {preview?.content?.trim() ? preview.content : "Содержимое не сохранено."}
          </div>
        </DialogContent>
      </Dialog>
      <Dialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null)
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Удалить источник?</DialogTitle>
            <DialogDescription>
              «{deleteTarget?.title ?? ""}» и связанные с ним цитаты исчезнут из исследования.
              Это действие нельзя отменить.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>
              Отмена
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                const target = deleteTarget
                setDeleteTarget(null)
                if (target) void remove(target.id)
              }}
            >
              Удалить
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={attachOpen} onOpenChange={setAttachOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>Добавить источник к прогону</DialogTitle>
            <DialogDescription>
              Стандарт или вакансия сразу попадут в run_sources: аналитик сможет цитировать
              источник, а синтезатор - находить конфликты между источниками.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="space-y-1.5">
              <Label>Тип источника</Label>
              <div className="flex gap-2">
                <Button
                  type="button"
                  size="sm"
                  variant={attachType === "standard" ? "secondary" : "outline"}
                  aria-pressed={attachType === "standard"}
                  onClick={() => setAttachType("standard")}
                >
                  Профстандарт
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant={attachType === "vacancy" ? "secondary" : "outline"}
                  aria-pressed={attachType === "vacancy"}
                  onClick={() => setAttachType("vacancy")}
                >
                  Вакансия
                </Button>
              </div>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="attach-source-title">Название</Label>
              <Input
                id="attach-source-title"
                value={attachTitle}
                onChange={(event) => setAttachTitle(event.target.value)}
                placeholder="Например: Профстандарт аналитика данных"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="attach-source-content">Текст источника</Label>
              <Textarea
                id="attach-source-content"
                value={attachContent}
                onChange={(event) => setAttachContent(event.target.value)}
                rows={6}
                placeholder="Вставьте требования, обязанности или текст вакансии..."
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="attach-source-url">Ссылка (необязательно)</Label>
              <Input
                id="attach-source-url"
                value={attachUrl}
                onChange={(event) => setAttachUrl(event.target.value)}
                placeholder="https://..."
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setAttachOpen(false)}>
              Отмена
            </Button>
            <Button onClick={() => void attachSource()} disabled={attachSaving}>
              {attachSaving && <Loader2Icon className="mr-1 size-4 animate-spin" />}
              Добавить
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

/**
 * Проверяет, является ли `candidateId` самим узлом `ancestorId` или его потомком, поднимаясь
 * по цепочке `parent_id` вверх. Нужен диалогу редактирования, чтобы в select родителей не
 * попадали узел и его поддерево - иначе перемещение создаёт цикл, который отклонит бэкенд
 * (`EVIDENCE_REQUIRED`/валидация дерева), но лучше не показывать такой вариант пользователю
 * вовсе. Множество `seen` страхует от уже присутствующего в данных цикла (иначе бесконечный
 * while).
 */
function isSelfOrDescendant(
  candidateId: string,
  ancestorId: string,
  nodes: ActivityNode[],
): boolean {
  const seen = new Set<string>()
  let current = nodes.find((node) => node.id === candidateId)
  while (current) {
    if (current.id === ancestorId) return true
    if (seen.has(current.id)) return false
    seen.add(current.id)
    const parentId = current.parent_id
    current = parentId ? nodes.find((node) => node.id === parentId) : undefined
  }
  return false
}

/**
 * Диалог редактирования узла: переименование, смена типа, описание и перемещение (select
 * родителя, очищенный {@link isSelfOrDescendant} от самого узла и его поддерева). Сохранение
 * идёт через `PATCH /nodes/{id}`; при ошибке валидации бэкенда показываем `error.message`
 * и не закрываем диалог, чтобы введённое не пропало.
 */
function NodeEditDialog({
  node,
  nodes,
  open,
  onOpenChange,
  onSaved,
}: {
  node: TreeNode | null
  nodes: ActivityNode[]
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const [title, setTitle] = useState(node?.title ?? "")
  const [description, setDescription] = useState(node?.description ?? "")
  const [type, setType] = useState<NodeType>(node?.type ?? "task")
  const [parentId, setParentId] = useState(node?.parent_id ?? "")
  const [saving, setSaving] = useState(false)

  const dirty =
    node !== null &&
    (title !== node.title ||
      description !== (node.description ?? "") ||
      type !== node.type ||
      parentId !== (node.parent_id ?? ""))

  const candidates = useMemo(() => {
    if (!node) return []
    return nodes.filter((candidate) => !isSelfOrDescendant(candidate.id, node.id, nodes))
  }, [node, nodes])

  const save = async () => {
    if (!node || !title.trim() || saving) return
    setSaving(true)
    const { error } = await apiClient.PATCH("/nodes/{id}", {
      params: { path: { id: node.id } },
      body: {
        type,
        title: title.trim(),
        description: description.trim(),
        parent_id: parentId || undefined,
      },
    })
    setSaving(false)
    if (error) {
      toast.error("Не удалось сохранить узел", { description: errorMessage(error) })
      return
    }
    onOpenChange(false)
    onSaved()
    toast.success("Узел обновлён")
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="sm:max-w-md"
        onEscapeKeyDown={(event) => {
          if (dirty) event.preventDefault()
        }}
        onPointerDownOutside={(event) => {
          if (dirty) event.preventDefault()
        }}
      >
        <DialogHeader>
          <DialogTitle>Редактировать узел</DialogTitle>
          <DialogDescription>
            Переименуйте узел, уточните описание или перенесите его в другое место дерева.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="node-title">Название</Label>
            <Input
              id="node-title"
              value={title}
              onChange={(event) => setTitle(event.target.value)}
            />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="node-type">Тип</Label>
              <Select value={type} onValueChange={(value) => setType(value as NodeType)}>
                <SelectTrigger id="node-type">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {(Object.keys(NODE_TYPE_LABELS) as NodeType[]).map((key) => (
                    <SelectItem key={key} value={key}>
                      {NODE_TYPE_LABELS[key]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="node-parent">Родительский узел</Label>
              <Select
                value={parentId === "" ? ROOT_PARENT : parentId}
                onValueChange={(value) => setParentId(value === ROOT_PARENT ? "" : value)}
              >
                <SelectTrigger id="node-parent">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={ROOT_PARENT}>- верхний уровень -</SelectItem>
                  {candidates.map((candidate) => (
                    <SelectItem key={candidate.id} value={candidate.id}>
                      {candidate.title}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="space-y-2">
            <Label htmlFor="node-description">Описание</Label>
            <Textarea
              id="node-description"
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              rows={3}
              placeholder="Что входит в этот элемент деятельности"
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Отмена
          </Button>
          <Button onClick={() => void save()} disabled={!title.trim() || saving}>
            {saving && <Loader2Icon className="size-4 animate-spin" />}
            Сохранить
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Диалог прикрепления обоснования: выбор источника и ввод цитаты. Мутация двухшаговая -
 * сначала `POST /evidences` (создать цитату), затем `PATCH /nodes/{id}` с полным списком
 * evidence узла (привязать). Если второй шаг упал, цитата на сервере осталась, но не
 * привязана - сообщаем это отдельным текстом, не делая вид, что всё успешно: честная
 * деградация важнее ложного успеха.
 */
function EvidenceAttachDialog({
  node,
  open,
  onOpenChange,
  onAttached,
}: {
  node: TreeNode | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onAttached: () => void
}) {
  const [sources, setSources] = useState<Source[]>([])
  const [loadingSources, setLoadingSources] = useState(true)
  const [sourceId, setSourceId] = useState("")
  const [quote, setQuote] = useState("")
  const [saving, setSaving] = useState(false)
  const attachDirty = Boolean(sourceId || quote.trim())

  useEffect(() => {
    if (!open) return
    const load = async () => {
      setLoadingSources(true)
      const { data } = await apiClient.GET("/sources")
      setSources(data ?? [])
      setLoadingSources(false)
    }
    void load()
  }, [open])

  const attach = async () => {
    if (!node || !sourceId || !quote.trim() || saving) return
    setSaving(true)
    const { data: created, error: createError } = await apiClient.POST("/evidences", {
      body: { node_id: node.id, source_id: sourceId, quote: quote.trim() },
    })
    if (createError || !created) {
      setSaving(false)
      toast.error("Не удалось создать цитату", { description: errorMessage(createError) })
      return
    }
    const nextEvidence = [...node.evidence, created]
    const { error: patchError } = await apiClient.PATCH("/nodes/{id}", {
      params: { path: { id: node.id } },
      body: { evidence: nextEvidence },
    })
    setSaving(false)
    if (patchError) {
      toast.error("Цитата создана, но не привязана к узлу", {
        description: errorMessage(patchError),
      })
      return
    }
    onOpenChange(false)
    setQuote("")
    setSourceId("")
    onAttached()
    toast.success("Обоснование добавлено")
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="sm:max-w-md"
        onEscapeKeyDown={(event) => {
          if (attachDirty) event.preventDefault()
        }}
        onPointerDownOutside={(event) => {
          if (attachDirty) event.preventDefault()
        }}
      >
        <DialogHeader>
          <DialogTitle>Добавить обоснование</DialogTitle>
          <DialogDescription>
            Выберите источник и вставьте цитату, которая подтверждает этот элемент деятельности.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="attach-source">Источник</Label>
            {loadingSources ? (
              <Skeleton className="h-9 w-full" />
            ) : sources.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                Сначала добавьте источник на вкладке «Источники».
              </p>
            ) : (
              <Select value={sourceId} onValueChange={setSourceId}>
                <SelectTrigger id="attach-source">
                  <SelectValue placeholder="- выберите источник -" />
                </SelectTrigger>
                <SelectContent>
                  {sources.map((source) => (
                    <SelectItem key={source.id} value={source.id}>
                      {source.title}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>
          <div className="space-y-2">
            <Label htmlFor="attach-quote">Цитата</Label>
            <Textarea
              id="attach-quote"
              value={quote}
              onChange={(event) => setQuote(event.target.value)}
              rows={3}
              placeholder="Дословная выдержка из источника…"
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Отмена
          </Button>
          <Button
            onClick={() => void attach()}
            disabled={!sourceId || !quote.trim() || saving}
          >
            {saving && <Loader2Icon className="size-4 animate-spin" />}
            Прикрепить
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Вкладка «Дерево»: грузит плоский список узлов, поллит его пока прогон активен, владеет
 * состоянием подтверждённых цитат и диалогами редактирования/прикрепления. Сам список строк
 * и панель поиска/фильтров рисует {@link ActivityTree}.
 *
 * Ключевая строка - JSON‑guard в `load`: новое дерево сравнивается со старым по JSON и
 * прежняя ссылка сохраняется при идентичном содержимом. Без этого поллинг каждые 5 секунд
 * подставлял бы новый массив, пересчитывал `roots` в дереве и сбрасывал раскрытые ветки -
 * видимый баг на активном прогоне, а не микрооптимизация.
 *
 * @param id Id прогона из URL.
 * @param active Флаг «прогон ещё выполняется»: включает поллинг; на завершённом прогоне
 * дерево грузится один раз.
 */
function TreeTab({
  id,
  active,
  focusNodeId,
  onShowTranscript,
  readOnly = false,
}: {
  id: string
  active: boolean
  focusNodeId?: string | null
  onShowTranscript?: (item: Evidence, source?: Source) => void
  readOnly?: boolean
}) {
  const [nodes, setNodes] = useState<ActivityNode[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState<TreeNode | null>(null)
  const [attaching, setAttaching] = useState<TreeNode | null>(null)
  const [approvedIds, setApprovedIds] = useState<Set<string>>(() => new Set())
  const [statuses, setStatuses] = useState<Map<string, "pending" | "approved" | "rejected">>(
    () => new Map(),
  )
  const [sourcesById, setSourcesById] = useState<Map<string, Source>>(() => new Map())
  const [insights, setInsights] = useState<Insights | null>(null)
  const [cost, setCost] = useState<RunCost | null>(null)
  const [zunOpen, setZunOpen] = useState(false)
  const loadInsights = useCallback(async () => {
    try {
      const { data } = await apiClient.GET("/runs/{id}/insights", {
        params: { path: { id } },
      })
      if (data && Array.isArray(data.flags)) setInsights(data)
    } catch {
      // Инсайты необязательны: дерево и без них остаётся рабочим.
    }
  }, [id])

  useEffect(() => {
    let cancelled = false
    const loadCost = async () => {
      try {
        const { data } = await apiClient.GET("/runs/{id}/cost", { params: { path: { id } } })
        if (!cancelled && data) setCost(data)
      } catch {
        // Стоимость необязательна для работы дерева.
      }
    }
    void loadCost()
    if (!active) {
      return () => {
        cancelled = true
      }
    }
    const timer = window.setInterval(() => void loadCost(), 5000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [id, active])

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const { data } = await apiClient.GET("/sources")
        if (!cancelled && Array.isArray(data)) setSourcesById(buildSourceIndex(data))
      } catch {
        // Метаданные источников необязательны: дерево работает и без типов/приоритетов.
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  const load = useCallback(async () => {
    try {
      const { data } = await apiClient.GET("/runs/{id}/tree", {
        params: { path: { id } },
      })
      if (data) {
        const nextNodes = Array.isArray(data) ? data : (data.nodes ?? [])
        setNodes((prev) => (JSON.stringify(prev) === JSON.stringify(nextNodes) ? prev : nextNodes))
        if (!Array.isArray(data) && data.insights) {
          setInsights(data.insights)
        } else {
          // Совместимость со старым бэкендом, где /tree возвращал только массив.
          void loadInsights()
        }
      }
      setError(null)
    } catch {
      setError("Не удалось получить дерево: бэкенд недоступен")
    } finally {
      setLoading(false)
    }
  }, [id, loadInsights])

  useEffect(() => {
    // Загрузка дерева меняет состояние после ответа API и нужна при открытии вкладки.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load()
    if (!active) return
    const timer = setInterval(() => void load(), 5000)
    return () => clearInterval(timer)
  }, [active, load])

  // Экспорт отдаёт бэкенд: в файл попадают evidence, конфликты и финальное сообщение,
  // которых нет в клиентской сборке дерева.
  const downloadExport = async (format: "md" | "json") => {
    const token = getValidToken()
    try {
      const response = await fetch(
        `${apiBaseUrl}/runs/${encodeURIComponent(id)}/export?format=${format}`,
        { headers: token ? { Authorization: `Bearer ${token}` } : undefined },
      )
      if (!response.ok) {
        const payload = (await response.json().catch(() => null)) as { message?: string } | null
        toast.error("Не удалось скачать экспорт", {
          description: payload?.message ?? `HTTP ${response.status}`,
        })
        return
      }
      const text = await response.text()
      downloadText(
        format === "md" ? "run.md" : "run.json",
        text,
        format === "md" ? "text/markdown;charset=utf-8" : "application/json;charset=utf-8",
      )
    } catch {
      toast.error("Сервер недоступен", { description: "Экспорт не скачан." })
    }
  }

  /**
   * Подтверждение цитаты с optimistic‑rollback: id сначала добавляется в `approvedIds`
   * (мгновенная реакция UI), затем уходит `POST /evidences/{id}/approve`; при ошибке id
   * убирается обратно и показывается `error.message`. Откат обязателен, иначе интерфейс
   * показал бы «Подтверждено» на непрошедшей операции.
   */
  const changeStatus = useCallback(async (evidenceId: string, action: "approve" | "reject") => {
    const next = action === "approve" ? "approved" : "rejected"
    setStatuses((prev) => new Map(prev).set(evidenceId, next))

    const result =
      action === "approve"
        ? await apiClient.POST("/evidences/{id}/approve", {
            params: { path: { id: evidenceId } },
          })
        : await apiClient.POST("/evidences/{id}/reject", {
            params: { path: { id: evidenceId } },
          })

    if (result.error) {
      setStatuses((prev) => {
        const copy = new Map(prev)
        copy.delete(evidenceId)
        return copy
      })
      toast.error(
        action === "approve" ? "Не удалось подтвердить цитату" : "Не удалось отклонить цитату",
        { description: errorMessage(result.error) },
      )
      return
    }

    setApprovedIds((prev) => {
      const copy = new Set(prev)
      if (action === "approve") copy.add(evidenceId)
      else copy.delete(evidenceId)
      return copy
    })
    toast.success(action === "approve" ? "Цитата подтверждена" : "Цитата отклонена")
  }, [])

  const approve = useCallback(
    (evidenceId: string) => void changeStatus(evidenceId, "approve"),
    [changeStatus],
  )
  const reject = useCallback(
    (evidenceId: string) => void changeStatus(evidenceId, "reject"),
    [changeStatus],
  )

  if (loading) {
    return (
      <div className="space-y-2">
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-10 w-2/3" />
        <Skeleton className="h-10 w-1/2" />
      </div>
    )
  }

  if (error) {
    return (
      <div className="flex flex-col rounded-xl border">
        <EmptyState icon={NetworkIcon} title="Дерево недоступно" description={error} />
      </div>
    )
  }

  if (nodes.length === 0) {
    return (
      <div className="flex flex-col rounded-xl border">
        <EmptyState
          icon={NetworkIcon}
          title="Дерево ещё не построено"
          description="Дождитесь завершения запуска - агенты сформируют структуру деятельности."
        />
      </div>
    )
  }

  return (
    <>
      {cost && cost.request_count > 0 && (
        <div className="mb-3 flex flex-wrap items-center gap-x-4 gap-y-1 rounded-lg border bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
          <span>Запросов: {cost.request_count}</span>
          <span>Токенов: {cost.total_tokens.toLocaleString("ru-RU")}</span>
          {cost.duration_seconds !== null && (
            <span>
              Длительность: {Math.max(1, Math.round(cost.duration_seconds / 60))} мин
            </span>
          )}
          <span>
            Стоимость:{" "}
            {cost.cost_usd === null
              ? "неизвестна"
              : `$${cost.cost_usd.toLocaleString("ru-RU", { maximumFractionDigits: 4 })}`}
          </span>
          {cost.estimate && (
            <span>
              Оценка: {cost.estimate.estimated_tokens.toLocaleString("ru-RU")} токенов
              {cost.estimate.estimated_tokens > 0 && (
                <>
                  {" "}
                  · отклонение{" "}
                  {Math.round(
                    ((cost.total_tokens - cost.estimate.estimated_tokens) /
                      cost.estimate.estimated_tokens) *
                      100,
                  )}
                  %
                </>
              )}
              {cost.duration_seconds !== null &&
                cost.estimate.estimated_duration_seconds > 0 && (
                  <>
                    {" "}
                    · время{" "}
                    {Math.round(
                      ((cost.duration_seconds - cost.estimate.estimated_duration_seconds) /
                        cost.estimate.estimated_duration_seconds) *
                        100,
                    )}
                    %
                  </>
                )}
            </span>
          )}
        </div>
      )}
      <div className="mb-3 flex justify-end gap-2" data-print-hide>
        {!readOnly && (
        <Button
          variant="outline"
          size="sm"
          className="h-7 gap-1 px-2.5 text-xs"
          onClick={() => setZunOpen(true)}
        >
          <SparklesIcon className="size-3.5" />
          Предложить ЗУН
        </Button>
        )}
        <Button
          variant="outline"
          size="sm"
          className="h-7 px-2.5 text-xs"
          onClick={() => void downloadExport("md")}
        >
          <DownloadIcon className="size-3.5" />
          Markdown
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="h-7 px-2.5 text-xs"
          onClick={() => void downloadExport("json")}
        >
          <DownloadIcon className="size-3.5" />
          JSON
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="h-7 px-2.5 text-xs"
          onClick={() => window.print()}
        >
          <PrinterIcon className="size-3.5" />
          Печать / PDF
        </Button>
      </div>
      <ActivityTree
        nodes={nodes}
        approvedIds={approvedIds}
        statuses={statuses}
        onApprove={approve}
        onReject={reject}
        onEdit={setEditing}
        onAttach={setAttaching}
        focusNodeId={focusNodeId}
        sourcesById={sourcesById}
        onShowTranscript={onShowTranscript}
        insights={insights}
        readOnly={readOnly}
      />
      <NodeEditDialog
        key={editing?.id ?? "closed"}
        node={editing}
        nodes={nodes}
        open={editing !== null}
        onOpenChange={(open) => {
          if (!open) setEditing(null)
        }}
        onSaved={() => {
          void load()
        }}
      />
      <EvidenceAttachDialog
        node={attaching}
        open={attaching !== null}
        onOpenChange={(open) => {
          if (!open) setAttaching(null)
        }}
        onAttached={() => {
          void load()
        }}
      />
      <ZunSuggestionsDialog
        runId={id}
        open={zunOpen}
        onOpenChange={setZunOpen}
        onApplied={() => {
          void load()
        }}
      />
    </>
  )
}

/**
 * Компонент роута `/research/:id`. Загружает прогон, поллит статус пока он активен,
 * отслеживает смену статуса для тоста «завершён/ошибка» и рендерит шапку с вкладками.
 * Ошибка загрузки прогона показывается баннером с кнопкой «Повторить» и подсказкой про
 * мок‑сервер - страница не притворяется рабочей без данных.
 */
export function Research() {
  const { id = "" } = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const focusNodeId = searchParams.get("node")
  const guestView = searchParams.get("view") === "guest"
  const role = getCurrentRole()
  const readOnly = guestView || role === "guest" || role === "student"
  const canAttach =
    !readOnly && (role === null || role === "admin" || role === "teacher")
  const [chatFocus, setChatFocus] = useState<{ refId?: string; quote: string } | null>(null)
  const copyShareLink = async () => {
    const link = `${window.location.origin}/research/${id}?view=guest`
    try {
      await navigator.clipboard.writeText(link)
      toast.success("Ссылка для просмотра скопирована", {
        description: "Гость увидит дерево и цитаты без права изменений.",
      })
    } catch {
      toast.error("Не удалось скопировать ссылку")
    }
  }
  const [tab, setTab] = useState(focusNodeId ? "tree" : "sources")
  const showTranscript = useCallback(
    async (item: Evidence) => {
      let refId = item.ref_id
      let quote = item.quote
      try {
        const { data } = await apiClient.GET(
          "/runs/{id}/evidence/{evidence_id}/context",
          { params: { path: { id, evidence_id: item.id } } },
        )
        if (data?.match) {
          refId = data.match.message_id || refId
          quote = data.match.quote || quote
        }
      } catch {
        // Контекст необязателен: остаётся переход по ref_id/тексту цитаты.
      }
      setChatFocus({ refId, quote })
      setTab("chat")
    },
    [id],
  )
  /**
   * Переход из вкладки «Снимки и проверка» к узлу дерева: переключаем вкладку и
   * дублируем id узла в search-параметре `?node=`, который уже умеет читать
   * {@link ActivityTree} (раскрывает ветку и подсвечивает строку). Это же делает
   * ссылку на узел копируемой и переживающей перезагрузку страницы.
   */
  const focusNode = useCallback(
    (nodeId: string) => {
      setTab("tree")
      setSearchParams((previous) => {
        const next = new URLSearchParams(previous)
        next.set("node", nodeId)
        return next
      })
    },
    [setSearchParams, setTab],
  )
  const {
    run,
    loading,
    error,
    isActive,
    reload: loadRun,
  } = useRunPolling(id, {
    onStatusChange: (previous, next) => {
      if (previous === next) return
      if (next === "done") {
        toast.success("Запуск завершён", { description: "Дерево деятельности готово." })
      }
      if (next === "failed") {
        toast.error("Запуск завершился ошибкой", {
          description: "Попробуйте запустить исследование ещё раз.",
        })
      }
    },
  })

  return (
    <div className="space-y-6">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2.5">
          {loading ? (
            <Skeleton className="h-8 w-64" />
          ) : (
            <h1 className="break-words text-2xl font-semibold tracking-tight sm:text-3xl">
              {run?.profession ?? "Исследование"}
            </h1>
          )}
          <span className="inline-flex translate-y-[6px]">
            <StatusBadge status={run?.status} />
          </span>
          <div className="ml-auto flex items-center gap-2" data-print-hide>
            <Button
              variant="outline"
              size="sm"
              aria-label="Поделиться ссылкой"
              onClick={() => void copyShareLink()}
            >
              <Share2Icon className="size-3.5" />
              <span className="hidden sm:inline">Поделиться</span>
            </Button>
            <Button variant="outline" size="sm" aria-label="Печать" onClick={() => window.print()}>
              <PrinterIcon className="size-3.5" />
              <span className="hidden sm:inline">Печать</span>
            </Button>
          </div>
        </div>
        {run?.started_at && (
          <p className="mt-6 text-sm text-muted-foreground">
            Запущено {formatDate(run.started_at).replace(/\.$/, "")}
          </p>
        )}
        {run?.stage && run.status !== "done" && (
          <div className="mt-3">
            <RunStageStepper stage={run.stage} failed={run.status === "failed"} />
          </div>
        )}
        {run?.status === "degraded" && (
          <p className="mt-3 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-200">
            Результат неполный: агенты работали с ограничениями.
          </p>
        )}
        {run?.status === "failed" && (
          <p className="mt-3 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
            Прогон завершился ошибкой. Подробности - в журнале LLM-запросов админки.
          </p>
        )}
      </div>
      {error && !run && (
        <div className="space-y-3 rounded-xl border border-destructive/50 p-8 text-center">
          <p className="text-sm text-destructive">{error}</p>
          <p className="text-sm text-muted-foreground">
            Запустите бэкенд или мок‑сервер: node tests/mock-server.mjs
          </p>
          <Button variant="outline" onClick={() => void loadRun()}>
            Повторить
          </Button>
        </div>
      )}
      {isActive && (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2Icon className="size-3.5 animate-spin" />
          Агенты работают - страница обновляется автоматически
        </p>
      )}
      {guestView && (
        <p className="flex items-center gap-2 rounded-lg border border-primary/30 bg-primary/5 px-3 py-2 text-sm text-muted-foreground">
          <EyeIcon className="size-4 text-primary" />
          Режим просмотра: дерево и цитаты доступны без изменений.
        </p>
      )}
      <div className="hidden print:block">
        <p className="text-xs uppercase tracking-wide text-muted-foreground">Activity Descriptor</p>
        <p className="text-xl font-semibold" aria-hidden="true">
          {run?.profession ?? "Исследование"}
        </p>
        {run?.started_at && (
          <p className="text-sm text-muted-foreground">Старт: {formatDate(run.started_at)}</p>
        )}
      </div>
      <Tabs value={tab} onValueChange={setTab} className="flex-col">
        <TabsList className="h-9 items-center" data-print-hide>
          <TabsTrigger value="sources" className={TAB_TRIGGER_CLASS}>
            Источники
          </TabsTrigger>
          <TabsTrigger value="tree" className={TAB_TRIGGER_CLASS}>
            Дерево
          </TabsTrigger>
          <TabsTrigger value="coverage" className={TAB_TRIGGER_CLASS}>
            Покрытие
          </TabsTrigger>
          <TabsTrigger value="snapshots" className={TAB_TRIGGER_CLASS}>
            Снимки и проверка
          </TabsTrigger>
          <TabsTrigger value="conflicts" className={TAB_TRIGGER_CLASS}>
            Конфликты
          </TabsTrigger>
          <TabsTrigger value="chat" className={TAB_TRIGGER_CLASS}>
            Чат
          </TabsTrigger>
        </TabsList>
        <TabsContent value="sources">
          <SourcesTab runId={id} canAttach={canAttach} readOnly={readOnly} />
        </TabsContent>
        <TabsContent value="tree">
          <TreeTab
            id={id}
            active={isActive}
            focusNodeId={focusNodeId}
            onShowTranscript={showTranscript}
            readOnly={readOnly}
          />
        </TabsContent>
        <TabsContent value="coverage">
          <CoverageTab runId={id} />
        </TabsContent>
        <TabsContent value="snapshots">
          <SnapshotReviewTab runId={id} onFocusNode={focusNode} readOnly={readOnly} />
        </TabsContent>
        <TabsContent value="conflicts">
          <ConflictsTab runId={id} onFocusNode={focusNode} />
        </TabsContent>
        <TabsContent value="chat">
          <ChatTab
            runId={id}
            active={!loading && !error}
            runStatus={run?.status}
            stage={run?.stage}
            focus={chatFocus}
            readOnly={readOnly}
          />
        </TabsContent>
      </Tabs>
    </div>
  )
}
