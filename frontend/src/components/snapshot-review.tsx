/**
 * @file snapshot-review.tsx
 * @description Вкладка «Снимки и проверка» страницы исследования: сравнение двух
 * снимков дерева деятельности и очередь спорных подтверждений (disputed evidence).
 *
 * Два независимых блока:
 * 1. Сравнение снимков - `GET /runs/{id}/snapshots` и
 *    `GET /runs/{id}/snapshots/{head}/diff?against={base}`. По умолчанию сравниваются
 *    два последних снимка; показываются сводка, добавленные/удалённые узлы и изменения
 *    по полям (было/стало). У каждой цитаты видны source_id и ref_id, клик открывает
 *    диалог исходной цитаты с источником и переходом к узлу в дереве.
 * 2. Очередь проверки - `GET /reviews/evidence?status=...`; строку можно подтвердить
 *    или отклонить существующими `POST /evidences/{id}/approve|reject` (204). После
 *    успешной мутации строка уходит из текущего фильтра, при ошибке остаётся и
 *    показывается тост. Очередь доступна только администратору, поэтому 403
 *    выводится спокойной подсказкой, а не падением вкладки.
 *
 * Эндпоинты снимков и очереди пока отсутствуют в сгенерированной OpenAPI-схеме,
 * поэтому ходят через raw-fetch обёртки {@link listRunSnapshots},
 * {@link compareRunSnapshots}, {@link listReviewEvidence} из lib/api/client.
 *
 * Загрузка данных - асинхронная, с флагом отмены: смена вкладки/фильтра не даёт
 * «позднему» ответу перезаписать свежее состояние. Мутации кнопок - по клику, с
 * отдельным busy-состоянием, чтобы не отправлять один запрос дважды.
 *
 * Экспорт: {@link SnapshotReviewTab}.
 */
import { useCallback, useEffect, useRef, useState, type KeyboardEvent } from "react"
import { Link } from "react-router-dom"
import { toast } from "sonner"
import {
  CheckIcon,
  ChevronDownIcon,
  CopyIcon,
  ExternalLinkIcon,
  FileDiffIcon,
  GitCompareIcon,
  Loader2Icon,
  NetworkIcon,
  RefreshCwIcon,
  ShieldCheckIcon,
  XIcon,
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
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { NODE_TYPE_LABELS } from "@/components/activity-tree-model"
import {
  apiClient,
  ApiRequestError,
  compareRunSnapshots,
  listReviewEvidence,
  listRunSnapshots,
  type ActivityNode,
  type Evidence,
  type ReviewEvidenceStatus,
  type ReviewQueueItem,
  type ReviewProgress,
  type RunSnapshot,
  type SnapshotDiff,
  type SnapshotNodeChange,
  type Source,
} from "@/lib/api/client"
import { safeHttpUrl } from "@/lib/security"
import { buildSnapshotStory } from "@/lib/snapshot-story"

/** Размер страницы очереди проверки: контракт допускает 1..200. */
const REVIEW_PAGE_LIMIT = 50

/** Русские подписи изменённых полей из ответа сравнения. */
const SNAPSHOT_FIELD_LABELS: Record<string, string> = {
  title: "Название",
  description: "Описание",
  type: "Тип",
  parent_id: "Родитель",
  evidence: "Обоснования",
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

/** Краткая информация об источнике для диалога цитаты (из `GET /sources`). */
interface SourceInfo {
  title: string
  url?: string
  content?: string
}

/**
 * Данные для диалога «Исходная цитата». Собирается и из узлов сравнения
 * (там есть только source_id), и из строк очереди проверки (там есть готовые
 * source_title/source_url), поэтому поля источника необязательные.
 */
interface QuoteDetail {
  quote: string
  sourceId: string
  sourceTitle?: string
  sourceUrl?: string
  sourceContent?: string
  refId?: string
  status?: string
  nodeId: string
  nodeTitle?: string
  /** Заполняется для строк очереди, которые могут относиться к другому прогону. */
  runId?: string
}

/**
 * Форматирует дату ISO в короткую русскую форму с временем.
 * Неразобранное значение возвращается как есть, чтобы битые данные не роняли вкладку.
 */
function formatDateTime(value?: string) {
  if (!value) return ""
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat("ru-RU", {
    day: "numeric",
    month: "short",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date)
}

/** Сортирует снимки по возрастанию версии: последний элемент - самая новая версия. */
function sortSnapshots(snapshots: RunSnapshot[]): RunSnapshot[] {
  return [...snapshots].sort(
    (left, right) =>
      left.version - right.version || (left.created_at ?? "").localeCompare(right.created_at ?? ""),
  )
}

/** Подпись снимка в select: версия и дата создания, если она есть. */
function snapshotLabel(snapshot: RunSnapshot) {
  const created = formatDateTime(snapshot.created_at)
  return created ? `v${snapshot.version} · ${created}` : `v${snapshot.version}`
}

/** Сокращает длинный текст источника для предпросмотра в диалоге. */
function shorten(value: string, limit: number) {
  const trimmed = value.trim()
  if (trimmed.length <= limit) return trimmed
  return `${trimmed.slice(0, limit).trimEnd()}…`
}

/**
 * Человекочитаемое сообщение для ошибок raw-запросов: статусы 403/501/0
 * объясняют причину точнее, чем текст сервера.
 */
function describeRequestError(error: unknown): string {
  if (error instanceof ApiRequestError) {
    if (error.status === 403) {
      return "Недостаточно прав: раздел доступен только администратору."
    }
    if (error.status === 501) {
      return "Раздел недоступен: бэкенд работает без PostgreSQL."
    }
    if (error.status === 0) {
      return "Сервер недоступен: проверьте соединение."
    }
    return error.message
  }
  if (error instanceof Error && error.message) return error.message
  return "Неизвестная ошибка запроса."
}

/** Источники прогона: нужны, чтобы показать название и ссылку в диалоге цитаты. */
async function fetchSourceMap(): Promise<Map<string, SourceInfo>> {
  const map = new Map<string, SourceInfo>()
  try {
    const { data } = await apiClient.GET("/sources")
    // Бэкенд отдаёт Source с source_url, но в сгенерированной схеме поля пока нет -
    // читаем его защитно, чтобы не терять ссылку на источник.
    const sources = (data ?? []) as Array<Source & { source_url?: string }>
    for (const source of sources) {
      map.set(source.id, {
        title: source.title,
        url: safeHttpUrl(source.source_url) ?? undefined,
        content: source.content,
      })
    }
  } catch {
    // Источники - вспомогательные данные: сравнение снимков работает и без них.
  }
  return map
}

/** Загружает список снимков и карту источников одним параллельным запросом. */
async function fetchSnapshotData(runId: string): Promise<{
  snapshots: RunSnapshot[]
  sources: Map<string, SourceInfo>
}> {
  const [snapshots, sources] = await Promise.all([listRunSnapshots(runId), fetchSourceMap()])
  return {
    snapshots: Array.isArray(snapshots) ? sortSnapshots(snapshots) : [],
    sources,
  }
}

/** Одна строка «было → стало» в изменённом узле. */
function FieldChange({ label, before, after }: { label: string; before: string; after: string }) {
  return (
    <div className="grid gap-0.5 sm:grid-cols-[110px_1fr] sm:gap-3">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="space-y-0.5">
        <span className="block text-muted-foreground line-through decoration-destructive/60">
          {before || "-"}
        </span>
        <span className="block">{after || "-"}</span>
      </dd>
    </div>
  )
}

/**
 * Список цитат узла: каждая строка кликабельна и открывает диалог исходной
 * цитаты. Source_id и ref_id показаны явно - это требование «перехода к исходным
 * цитатам»: эксперт видит, откуда именно взята формулировка.
 */
function EvidenceQuotes({
  nodeId,
  nodeTitle,
  evidence,
  sources,
  onOpen,
}: {
  nodeId: string
  nodeTitle: string
  evidence: Evidence[]
  sources: Map<string, SourceInfo>
  onOpen: (detail: QuoteDetail) => void
}) {
  if (evidence.length === 0) {
    return <p className="text-xs text-muted-foreground">Обоснований нет.</p>
  }
  return (
    <ul className="space-y-1.5">
      {evidence.map((item) => {
        const source = sources.get(item.source_id)
        return (
          <li key={item.id}>
            <button
              type="button"
              onClick={() =>
                onOpen({
                  quote: item.quote,
                  sourceId: item.source_id,
                  sourceTitle: source?.title,
                  sourceUrl: source?.url,
                  sourceContent: source?.content,
                  refId: item.ref_id,
                  status: item.status,
                  nodeId,
                  nodeTitle,
                })
              }
              aria-label={`Открыть цитату: ${item.quote}`}
              className="flex w-full flex-wrap items-center gap-x-2 gap-y-1 rounded-lg border bg-muted/30 px-3 py-2 text-left text-sm transition-colors hover:border-primary/40 hover:bg-muted/50"
            >
              <span className="min-w-0 flex-1 whitespace-pre-wrap break-words text-muted-foreground">{item.quote}</span>
              <span className="shrink-0 font-mono text-[11px] text-muted-foreground">
                source_id: {item.source_id}
              </span>
              {item.ref_id && (
                <span className="shrink-0 font-mono text-[11px] text-muted-foreground">
                  ref_id: {item.ref_id}
                </span>
              )}
              <span
                className={`shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium ${
                  EVIDENCE_STATUS_CLASS[item.status ?? "pending"]
                }`}
              >
                {EVIDENCE_STATUS_LABELS[item.status ?? "pending"]}
              </span>
            </button>
          </li>
        )
      })}
    </ul>
  )
}

/** Секция добавленных или удалённых узлов сравнения. */
function DiffNodeSection({
  title,
  nodes,
  sources,
  onOpenQuote,
  removed = false,
}: {
  title: string
  nodes: ActivityNode[]
  sources: Map<string, SourceInfo>
  onOpenQuote: (detail: QuoteDetail) => void
  removed?: boolean
}) {
  const [open, setOpen] = useState(false)
  return (
    <section className="space-y-2">
      <button
        type="button"
        className="flex w-full items-center gap-2 rounded-lg border bg-muted/20 px-3 py-2 text-left transition-colors hover:bg-muted/40"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <h3 className="text-sm font-medium">
          {title} <span className="text-muted-foreground">({nodes.length})</span>
        </h3>
        <ChevronDownIcon
          className={`ml-auto size-4 text-muted-foreground transition-transform ${open ? "rotate-180" : ""}`}
        />
      </button>
      {open && (
        <ul className="space-y-2">
          {nodes.map((node) => (
            <li key={node.id} className="space-y-2 rounded-lg border bg-muted/20 p-3">
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant="secondary">{NODE_TYPE_LABELS[node.type]}</Badge>
                <span
                  className={`text-sm font-medium ${removed ? "text-muted-foreground line-through" : ""}`}
                >
                  {node.title}
                </span>
                <code className="ml-auto text-[11px] text-muted-foreground">{node.id}</code>
              </div>
              {node.description && (
                <p className="text-sm text-muted-foreground">{node.description}</p>
              )}
              <EvidenceQuotes
                nodeId={node.id}
                nodeTitle={node.title}
                evidence={node.evidence}
                sources={sources}
                onOpen={onOpenQuote}
              />
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

/** Один изменённый узел: список изменившихся полей, «было/стало» и актуальные цитаты. */
function ChangedNode({
  change,
  sources,
  onOpenQuote,
}: {
  change: SnapshotNodeChange
  sources: Map<string, SourceInfo>
  onOpenQuote: (detail: QuoteDetail) => void
}) {
  const before = change.before
  const after = change.after
  return (
    <li className="space-y-2 rounded-lg border bg-muted/20 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium">{after.title || before.title}</span>
        {change.fields.map((field) => (
          <Badge key={field} variant="outline">
            {SNAPSHOT_FIELD_LABELS[field] ?? field}
          </Badge>
        ))}
        <code className="ml-auto text-[11px] text-muted-foreground">{change.id}</code>
      </div>
      <dl className="space-y-1.5">
        {change.fields.includes("title") && (
          <FieldChange label="Название" before={before.title} after={after.title} />
        )}
        {change.fields.includes("description") && (
          <FieldChange
            label="Описание"
            before={before.description ?? ""}
            after={after.description ?? ""}
          />
        )}
        {change.fields.includes("type") && (
          <FieldChange
            label="Тип"
            before={NODE_TYPE_LABELS[before.type]}
            after={NODE_TYPE_LABELS[after.type]}
          />
        )}
        {change.fields.includes("parent_id") && (
          <FieldChange
            label="Родитель"
            before={before.parent_id ?? "верхний уровень"}
            after={after.parent_id ?? "верхний уровень"}
          />
        )}
      </dl>
      {change.fields.includes("evidence") && (
        <p className="text-xs text-muted-foreground">
          Обоснования изменились - ниже актуальные цитаты снимка.
        </p>
      )}
      <EvidenceQuotes
        nodeId={after.id}
        nodeTitle={after.title}
        evidence={after.evidence}
        sources={sources}
        onOpen={onOpenQuote}
      />
    </li>
  )
}

/** Секция изменённых узлов сравнения. */
function ChangedNodesSection({
  changes,
  sources,
  onOpenQuote,
}: {
  changes: SnapshotNodeChange[]
  sources: Map<string, SourceInfo>
  onOpenQuote: (detail: QuoteDetail) => void
}) {
  return (
    <section className="space-y-2">
      <h3 className="text-sm font-medium">
        Изменённые узлы <span className="text-muted-foreground">({changes.length})</span>
      </h3>
      <ul className="space-y-2">
        {changes.map((change) => (
          <ChangedNode
            key={change.id}
            change={change}
            sources={sources}
            onOpenQuote={onOpenQuote}
          />
        ))}
      </ul>
    </section>
  )
}

/** Сводка сравнения: сколько узлов добавлено, удалено и изменено. */
function DiffSummaryBadges({ diff }: { diff: SnapshotDiff }) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Badge variant="outline" className="bg-success/15 text-success">
        +{diff.summary.added} добавлено
      </Badge>
      <Badge variant="outline" className="bg-destructive/15 text-destructive">
        -{diff.summary.removed} удалено
      </Badge>
      <Badge variant="outline" className="bg-warning/15 text-warning">
        ~{diff.summary.changed} изменено
      </Badge>
      <span className="text-xs text-muted-foreground">
        Снимок v{diff.base.version} → v{diff.head.version}
      </span>
    </div>
  )
}

/**
 * Диалог исходной цитаты: текст цитаты, источник (название/ссылка, если есть),
 * source_id, ref_id и статус проверки. Для цитат из текущего прогона есть переход
 * к узлу в дереве; для строк очереди другого прогона - ссылка на его страницу.
 */
function QuoteDetailDialog({
  detail,
  currentRunId,
  onClose,
  onFocusNode,
}: {
  detail: QuoteDetail | null
  currentRunId: string
  onClose: () => void
  onFocusNode?: (nodeId: string) => void
}) {
  const sourceUrl = safeHttpUrl(detail?.sourceUrl)
  const sameRun = !detail?.runId || detail.runId === currentRunId
  return (
    <Dialog
      open={detail !== null}
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Исходная цитата</DialogTitle>
          <DialogDescription>
            {detail?.nodeTitle ?? "Узел дерева"}
            {detail?.nodeId ? ` · узел ${detail.nodeId}` : ""}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <blockquote className="rounded-lg border bg-muted/30 p-3 text-sm">
            «{detail?.quote ?? ""}»
          </blockquote>
          <dl className="grid grid-cols-[110px_1fr] gap-x-3 gap-y-1.5 text-sm">
            <dt className="text-muted-foreground">Источник</dt>
            <dd className="break-words">{detail?.sourceTitle ?? detail?.sourceId ?? "-"}</dd>
            <dt className="text-muted-foreground">source_id</dt>
            <dd className="font-mono text-xs break-all">{detail?.sourceId ?? "-"}</dd>
            <dt className="text-muted-foreground">ref_id</dt>
            <dd className="font-mono text-xs break-all">{detail?.refId ?? "не указан"}</dd>
            <dt className="text-muted-foreground">Статус</dt>
            <dd>
              <span
                className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${
                  EVIDENCE_STATUS_CLASS[detail?.status ?? "pending"]
                }`}
              >
                {EVIDENCE_STATUS_LABELS[detail?.status ?? "pending"] ?? detail?.status}
              </span>
            </dd>
          </dl>
          {sourceUrl && (
            <a
              href={sourceUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 text-sm text-primary underline underline-offset-4"
            >
              Открыть источник
              <ExternalLinkIcon className="size-3.5" />
            </a>
          )}
          {detail?.sourceContent && (
            <div>
              <p className="mb-1 text-xs font-medium text-muted-foreground">Фрагмент источника</p>
              <p className="max-h-32 overflow-y-auto whitespace-pre-wrap rounded-lg border bg-muted/20 p-2 text-sm text-muted-foreground">
                {shorten(detail.sourceContent, 600)}
              </p>
            </div>
          )}
        </div>
        <DialogFooter>
          {detail?.runId && !sameRun ? (
            <Button variant="outline" asChild>
              <Link to={`/research/${detail.runId}?node=${encodeURIComponent(detail.nodeId)}`}>
                Открыть исследование
              </Link>
            </Button>
          ) : (
            <Button
              variant="outline"
              onClick={() => {
                const nodeId = detail?.nodeId
                onClose()
                if (nodeId) onFocusNode?.(nodeId)
              }}
              disabled={!onFocusNode || !detail?.nodeId}
            >
              <NetworkIcon className="size-4" />
              Показать в дереве
            </Button>
          )}
          <Button onClick={onClose}>Закрыть</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Блок сравнения снимков. Список снимков грузится один раз при монтировании вкладки
 * (и по кнопке обновления); выбор базовой/новой версии по умолчанию - два последних
 * снимка. Сравнение запускается явно кнопкой, чтобы не дёргать тяжёлый diff на
 * каждое переключение select.
 */
function SnapshotDiffSection({
  runId,
  onFocusNode,
}: {
  runId: string
  onFocusNode?: (nodeId: string) => void
}) {
  const [snapshots, setSnapshots] = useState<RunSnapshot[]>([])
  const [sources, setSources] = useState<Map<string, SourceInfo>>(() => new Map())
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [baseId, setBaseId] = useState<string | null>(null)
  const [headId, setHeadId] = useState<string | null>(null)
  const [diff, setDiff] = useState<SnapshotDiff | null>(null)
  const [comparing, setComparing] = useState(false)
  const [compareError, setCompareError] = useState<string | null>(null)
  const [detail, setDetail] = useState<QuoteDetail | null>(null)
  const compareSequence = useRef(0)

  const applySnapshotData = useCallback(
    (data: { snapshots: RunSnapshot[]; sources: Map<string, SourceInfo> }) => {
      setSnapshots(data.snapshots)
      setSources(data.sources)
      // По умолчанию сравниваем два последних снимка; уже выбранные версии
      // сохраняем, пока они есть в новом списке.
      setBaseId((previous) => {
        if (previous && data.snapshots.some((snapshot) => snapshot.id === previous)) {
          return previous
        }
        return data.snapshots.length >= 2 ? data.snapshots[data.snapshots.length - 2].id : null
      })
      setHeadId((previous) => {
        if (previous && data.snapshots.some((snapshot) => snapshot.id === previous)) {
          return previous
        }
        return data.snapshots.length >= 1 ? data.snapshots[data.snapshots.length - 1].id : null
      })
    },
    [],
  )

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      setLoading(true)
      try {
        const data = await fetchSnapshotData(runId)
        if (cancelled) return
        applySnapshotData(data)
        setLoadError(null)
      } catch (error) {
        if (cancelled) return
        setLoadError(describeRequestError(error))
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [runId, applySnapshotData])

  const reload = useCallback(async () => {
    setRefreshing(true)
    try {
      const data = await fetchSnapshotData(runId)
      applySnapshotData(data)
      setLoadError(null)
      setDiff(null)
      setCompareError(null)
    } catch (error) {
      setLoadError(describeRequestError(error))
    } finally {
      setRefreshing(false)
    }
  }, [runId, applySnapshotData])

  const canCompare = Boolean(baseId && headId && baseId !== headId)

  const compare = useCallback(async () => {
    if (!baseId || !headId || baseId === headId) return
    const sequence = ++compareSequence.current
    setComparing(true)
    setCompareError(null)
    try {
      const result = await compareRunSnapshots(runId, headId, baseId)
      if (sequence !== compareSequence.current) return
      setDiff(result)
    } catch (error) {
      if (sequence !== compareSequence.current) return
      setDiff(null)
      setCompareError(describeRequestError(error))
    } finally {
      if (sequence === compareSequence.current) setComparing(false)
    }
  }, [baseId, headId, runId])

  const changeBase = (value: string) => {
    setBaseId(value)
    setDiff(null)
    setCompareError(null)
  }

  const changeHead = (value: string) => {
    setHeadId(value)
    setDiff(null)
    setCompareError(null)
  }

  return (
    <Card className="gap-4 p-5">
      <CardHeader>
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="space-y-1">
            <CardTitle className="flex items-center gap-2">
              <GitCompareIcon className="size-4 shrink-0" />
              Сравнение снимков
            </CardTitle>
            <CardDescription>
              Выберите базовую и новую версии - увидите добавленные, удалённые и изменённые
              узлы дерева, включая обоснования с исходными цитатами.
            </CardDescription>
          </div>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Обновить список снимков"
            disabled={loading || refreshing}
            onClick={() => void reload()}
          >
            <RefreshCwIcon className={`size-4 ${refreshing ? "animate-spin" : ""}`} />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {loading ? (
          <div className="space-y-2">
            <Skeleton className="h-9 w-full" />
            <Skeleton className="h-9 w-2/3" />
            <Skeleton className="h-8 w-40" />
          </div>
        ) : loadError ? (
          <div className="space-y-3 rounded-lg border border-destructive/50 p-4 text-sm">
            <p className="text-destructive">{loadError}</p>
            <Button variant="outline" size="sm" onClick={() => void reload()}>
              Повторить
            </Button>
          </div>
        ) : snapshots.length === 0 ? (
          <div className="flex flex-col items-center gap-2 rounded-lg border py-10 text-center">
            <FileDiffIcon className="size-6 text-muted-foreground" />
            <p className="font-medium">Снимков пока нет</p>
            <p className="max-w-md text-sm text-muted-foreground">
              Снимок фиксирует состояние дерева исследования. Когда появятся две версии,
              здесь можно будет увидеть, что изменилось.
            </p>
          </div>
        ) : (
          <>
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="snapshot-base">Базовая версия</Label>
                <Select value={baseId ?? undefined} onValueChange={changeBase}>
                  <SelectTrigger id="snapshot-base" aria-label="Базовая версия">
                    <SelectValue placeholder="Выберите снимок" />
                  </SelectTrigger>
                  <SelectContent>
                    {snapshots.map((snapshot) => (
                      <SelectItem key={snapshot.id} value={snapshot.id}>
                        {snapshotLabel(snapshot)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-2">
                <Label htmlFor="snapshot-head">Новая версия</Label>
                <Select value={headId ?? undefined} onValueChange={changeHead}>
                  <SelectTrigger id="snapshot-head" aria-label="Новая версия">
                    <SelectValue placeholder="Выберите снимок" />
                  </SelectTrigger>
                  <SelectContent>
                    {snapshots.map((snapshot) => (
                      <SelectItem key={snapshot.id} value={snapshot.id}>
                        {snapshotLabel(snapshot)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Button
                onClick={() => void compare()}
                disabled={!canCompare || comparing}
                aria-label="Сравнить снимки"
              >
                {comparing ? (
                  <Loader2Icon className="size-4 animate-spin" />
                ) : (
                  <GitCompareIcon className="size-4" />
                )}
                {comparing ? "Сравнение…" : "Сравнить"}
              </Button>
              {snapshots.length === 1 && (
                <p className="text-xs text-muted-foreground">
                  Для сравнения нужен хотя бы второй снимок - сейчас доступна только одна версия.
                </p>
              )}
              {baseId !== null && baseId === headId && (
                <p className="text-xs text-muted-foreground">
                  Выберите разные версии: сравнивать снимок сам с собой нечего.
                </p>
              )}
            </div>
            {compareError && (
              <div className="rounded-lg border border-destructive/50 p-3 text-sm text-destructive">
                {compareError}
              </div>
            )}
            {diff && (
              <div className="space-y-4" data-testid="snapshot-diff">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <DiffSummaryBadges diff={diff} />
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-7 gap-1 px-2.5 text-xs"
                    onClick={() => {
                      void navigator.clipboard
                        ?.writeText(diff.story ?? buildSnapshotStory(diff))
                        .then(() => toast.success("Changelog скопирован"))
                        .catch(() => toast.error("Не удалось скопировать changelog"))
                    }}
                  >
                    <CopyIcon className="size-3.5" />
                    Скопировать changelog
                  </Button>
                </div>
                {diff.summary.added === 0 &&
                diff.summary.removed === 0 &&
                diff.summary.changed === 0 ? (
                  <p className="rounded-lg border bg-muted/20 p-4 text-sm text-muted-foreground">
                    Снимки совпадают: различий в дереве не найдено.
                  </p>
                ) : (
                  <div className="space-y-5">
                    {diff.added.length > 0 && (
                      <DiffNodeSection
                        title="Добавленные узлы"
                        nodes={diff.added}
                        sources={sources}
                        onOpenQuote={setDetail}
                      />
                    )}
                    {diff.removed.length > 0 && (
                      <DiffNodeSection
                        title="Удалённые узлы"
                        nodes={diff.removed}
                        sources={sources}
                        onOpenQuote={setDetail}
                        removed
                      />
                    )}
                    {diff.changed.length > 0 && (
                      <ChangedNodesSection
                        changes={diff.changed}
                        sources={sources}
                        onOpenQuote={setDetail}
                      />
                    )}
                  </div>
                )}
              </div>
            )}
          </>
        )}
      </CardContent>
      <QuoteDetailDialog
        detail={detail}
        currentRunId={runId}
        onClose={() => setDetail(null)}
        onFocusNode={onFocusNode}
      />
    </Card>
  )
}

/**
 * Блок очереди проверки спорных подтверждений. Показывает цитаты, источник, узел и
 * прогон; кнопки подтверждения/отклонения ходят в существующие endpoint'ы
 * (204 No Content) и после успеха убирают строку из текущего фильтра.
 */
function ReviewQueueSection({
  runId,
  onFocusNode,
}: {
  runId: string
  onFocusNode?: (nodeId: string) => void
}) {
  const [status, setStatus] = useState<ReviewEvidenceStatus>("pending")
  const [items, setItems] = useState<ReviewQueueItem[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [detail, setDetail] = useState<QuoteDetail | null>(null)
  const [reloadToken, setReloadToken] = useState(0)
  const [initialPending, setInitialPending] = useState(0)
  const [approvedCount, setApprovedCount] = useState(0)
  const [selectedIndex, setSelectedIndex] = useState(0)
  const [progress, setProgress] = useState<ReviewProgress | null>(null)
  const initialized = useRef(false)

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      setLoading(true)
      try {
        const [data, progressResult] = await Promise.all([
          listReviewEvidence(status, REVIEW_PAGE_LIMIT),
          apiClient.GET("/reviews/evidence/progress", {
            params: { query: { run_id: runId } },
          }),
        ])
        if (cancelled) return
        const rows = Array.isArray(data) ? data : []
        setItems(rows)
        const progressData = progressResult.data
        if (progressData && !Array.isArray(progressData) && typeof progressData.pending === "number") {
          setProgress(progressData)
        }
        if (status === "pending" && !initialized.current) {
          initialized.current = true
          setInitialPending(rows.length)
        }
        setLoadError(null)
      } catch (error) {
        if (cancelled) return
        setItems([])
        setLoadError(describeRequestError(error))
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [status, reloadToken, runId])

  const changeStatus = async (item: ReviewQueueItem, action: "approve" | "reject") => {
    if (busyId) return
    setBusyId(item.id)
    try {
      const result =
        action === "approve"
          ? await apiClient.POST("/evidences/{id}/approve", { params: { path: { id: item.id } } })
          : await apiClient.POST("/evidences/{id}/reject", { params: { path: { id: item.id } } })
      if (result.error) {
        toast.error(
          action === "approve" ? "Не удалось подтвердить цитату" : "Не удалось отклонить цитату",
          { description: result.error.message },
        )
        return
      }
      // Строка больше не относится к текущему фильтру: подтверждённая уходит из
      // «На проверке», отклонённая - из «Отклонённые» (и наоборот).
      setItems((previous) => previous.filter((row) => row.id !== item.id))
      setSelectedIndex((index) => Math.max(0, Math.min(index, items.length - 2)))
      if (action === "approve") setApprovedCount((count) => count + 1)
      setProgress((current) =>
        current
          ? {
              ...current,
              pending: Math.max(0, current.pending - 1),
              approved: action === "approve" ? current.approved + 1 : current.approved,
              rejected: action === "reject" ? current.rejected + 1 : current.rejected,
            }
          : current,
      )
      toast.success(action === "approve" ? "Цитата подтверждена" : "Цитата отклонена")
    } catch {
      toast.error("Сервер недоступен", { description: "Статус цитаты не изменён." })
    } finally {
      setBusyId(null)
    }
  }

  const openDetail = (item: ReviewQueueItem) => {
    setDetail({
      quote: item.quote,
      sourceId: item.source_id,
      sourceTitle: item.source_title,
      sourceUrl: item.source_url,
      refId: item.ref_id,
      status: item.status,
      nodeId: item.node_id,
      nodeTitle: item.node_title,
      runId: item.run_id,
    })
  }

  const onQueueKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    const tag = (event.target as HTMLElement).tagName
    if (["INPUT", "TEXTAREA", "SELECT"].includes(tag)) return
    const current = items[selectedIndex]
    if (event.key === "ArrowRight" || event.key === "ArrowDown" || event.key === "n" || event.key === "N") {
      event.preventDefault()
      setSelectedIndex((index) => Math.min(items.length - 1, index + 1))
      return
    }
    if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
      event.preventDefault()
      setSelectedIndex((index) => Math.max(0, index - 1))
      return
    }
    if (!current || busyId) return
    if (event.key === "a" || event.key === "A") {
      event.preventDefault()
      void changeStatus(current, "approve")
    }
    if (event.key === "r" || event.key === "R") {
      event.preventDefault()
      void changeStatus(current, "reject")
    }
  }

  return (
    <Card className="gap-4 p-5">
      <CardHeader>
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="space-y-1">
            <CardTitle className="flex items-center gap-2">
              <ShieldCheckIcon className="size-4 shrink-0" />
              Очередь проверки
            </CardTitle>
            <CardDescription>
              Спорные цитаты, которые ещё не проверены или были отклонены. Сверьте формулировку
              с источником и подтвердите либо отклоните её.
            </CardDescription>
          </div>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Обновить очередь проверки"
            disabled={loading}
            onClick={() => setReloadToken((token) => token + 1)}
          >
            <RefreshCwIcon className="size-4" />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex flex-wrap items-center gap-2">
          {(
            [
              ["pending", "На проверке"],
              ["rejected", "Отклонённые"],
            ] as const
          ).map(([value, label]) => (
            <Button
              key={value}
              size="sm"
              variant={status === value ? "secondary" : "outline"}
              className="h-7 px-2.5 text-xs"
              aria-pressed={status === value}
              onClick={() => setStatus(value)}
            >
              {label}
            </Button>
          ))}
          <span className="ml-auto text-xs text-muted-foreground">
            {loading
              ? "Загрузка…"
              : progress
                ? `Подтверждено ${progress.approved} из ${progress.total} · проверено ${progress.approved + progress.rejected}, на проверке ${progress.pending}`
                : status === "pending" && initialPending > 0
                  ? `Подтверждено ${approvedCount} из ${initialPending} · записей: ${items.length}`
                  : `Записей: ${items.length} (лимит ${REVIEW_PAGE_LIMIT})`}
          </span>
        </div>
        {progress && progress.total > 0 && (
          <div className="space-y-1">
            <div className="flex h-2 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full bg-success"
                style={{ width: `${(progress.approved / progress.total) * 100}%` }}
                title={`Подтверждено: ${progress.approved}`}
              />
              <div
                className="h-full bg-destructive"
                style={{ width: `${(progress.rejected / progress.total) * 100}%` }}
                title={`Отклонено: ${progress.rejected}`}
              />
              <div
                className="h-full bg-warning"
                style={{ width: `${(progress.pending / progress.total) * 100}%` }}
                title={`На проверке: ${progress.pending}`}
              />
            </div>
            <div className="flex flex-wrap gap-3 text-[11px] text-muted-foreground">
              <span>● подтверждено {progress.approved}</span>
              <span>● отклонено {progress.rejected}</span>
              <span>● на проверке {progress.pending}</span>
            </div>
          </div>
        )}
        <p className="text-[11px] text-muted-foreground">
          Горячие клавиши: A - подтвердить, R - отклонить, N или стрелки - выбрать следующую цитату.
        </p>
        {loading ? (
          <div className="space-y-2">
            {[0, 1, 2].map((row) => (
              <Skeleton key={row} className="h-20 w-full" />
            ))}
          </div>
        ) : loadError ? (
          <div className="space-y-3 rounded-lg border border-destructive/50 p-4 text-sm">
            <p className="text-destructive">{loadError}</p>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setReloadToken((token) => token + 1)}
            >
              Повторить
            </Button>
          </div>
        ) : items.length === 0 ? (
          <p className="rounded-lg border bg-muted/20 p-4 text-sm text-muted-foreground">
            {status === "pending"
              ? "Нет цитат, ожидающих проверки."
              : "Отклонённых цитат нет."}
          </p>
        ) : (
          <ul
            className="space-y-2 rounded-lg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/60"
            tabIndex={0}
            onKeyDown={onQueueKeyDown}
            aria-label="Очередь проверки"
          >
            {items.map((item, index) => {
              const sourceUrl = safeHttpUrl(item.source_url)
              const busy = busyId === item.id
              return (
                <li
                  key={item.id}
                  className={`space-y-2 rounded-lg border bg-muted/20 p-3 ${
                    index === selectedIndex ? "ring-2 ring-primary/40" : ""
                  }`}
                  onClick={(event) => {
                    setSelectedIndex(index)
                    ;(event.currentTarget.parentElement as HTMLElement | null)?.focus()
                  }}
                >
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge
                      variant="outline"
                      className={EVIDENCE_STATUS_CLASS[item.status]}
                    >
                      {EVIDENCE_STATUS_LABELS[item.status]}
                    </Badge>
                    {item.node_title && (
                      <span className="text-sm font-medium">{item.node_title}</span>
                    )}
                    <code className="text-[11px] text-muted-foreground">{item.node_id}</code>
                    <span className="ml-auto">
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-6 gap-1 px-2 text-xs"
                        aria-label={item.node_title ? `Открыть цитату узла ${item.node_title}` : "Открыть цитату"}
                        onClick={() => openDetail(item)}
                      >
                        Открыть
                      </Button>
                    </span>
                  </div>
                  <blockquote className="whitespace-pre-wrap break-words text-sm text-muted-foreground">«{item.quote}»</blockquote>
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                    <span>Источник: {item.source_title || item.source_id}</span>
                    <span className="font-mono">source_id: {item.source_id}</span>
                    {item.ref_id && <span className="font-mono">ref_id: {item.ref_id}</span>}
                    <Link
                      to={`/research/${item.run_id}?node=${encodeURIComponent(item.node_id)}`}
                      className="underline underline-offset-4 hover:text-foreground"
                    >
                      Перейти к узлу
                    </Link>
                    {sourceUrl && (
                      <a
                        href={sourceUrl}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="inline-flex items-center gap-1 underline underline-offset-4 hover:text-foreground"
                      >
                        Открыть источник
                        <ExternalLinkIcon className="size-3" />
                      </a>
                    )}
                    <span className="font-mono">прогон: {item.run_id}</span>
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={busy}
                      onClick={() => void changeStatus(item, "approve")}
                    >
                      {busy ? (
                        <Loader2Icon className="size-3.5 animate-spin" />
                      ) : (
                        <CheckIcon className="size-3.5" />
                      )}
                      Подтвердить
                    </Button>
                    {item.status !== "rejected" && (
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={busy}
                        onClick={() => void changeStatus(item, "reject")}
                      >
                        <XIcon className="size-3.5" />
                        Отклонить
                      </Button>
                    )}
                  </div>
                </li>
              )
            })}
          </ul>
        )}
      </CardContent>
      <QuoteDetailDialog
        detail={detail}
        currentRunId={runId}
        onClose={() => setDetail(null)}
        onFocusNode={onFocusNode}
      />
    </Card>
  )
}

/**
 * Вкладка «Снимки и проверка»: сравнение версий дерева и очередь спорных цитат.
 *
 * @param runId Id исследования из URL.
 * @param onFocusNode Переход к узлу на вкладке «Дерево» (страница Research меняет
 *   вкладку и search-параметр `?node=`); без колбэка кнопка перехода не показывается.
 */
export function SnapshotReviewTab({
  runId,
  onFocusNode,
  readOnly = false,
}: {
  runId: string
  onFocusNode?: (nodeId: string) => void
  readOnly?: boolean
}) {
  return (
    <div className="space-y-6">
      <SnapshotDiffSection runId={runId} onFocusNode={onFocusNode} />
      {!readOnly && <ReviewQueueSection runId={runId} onFocusNode={onFocusNode} />}
    </div>
  )
}
