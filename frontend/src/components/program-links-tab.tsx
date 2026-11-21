/**
 * @file program-links-tab.tsx
 * @description Вкладка «Программа»: связи узлов дерева деятельности с элементами
 * образовательной программы и решение методиста по каждой связи.
 *
 * Данные: `GET /runs/{id}/program-links` (опционально с `?node_id=`) и
 * `POST /runs/{id}/program-links/{link_id}/review` с телом `{status, reason}`.
 * После успешного решения список перезагружается, чтобы статус и группа не врали.
 *
 * Связи сгруппированы по статусу: «Не проверено» и «Подтверждено» видны сразу,
 * «Отклонённые» свёрнуты. Пустой список - нормальное состояние: предложения строит
 * ядро системы, поэтому экран объясняет это нейтрально и не называет отсутствие
 * связи недостатком программы. Ответ 501 означает, что сопоставление ещё не включено
 * на сервере: это не ошибка, а то же пустое состояние с пометкой.
 *
 * Экспорт: {@link ProgramLinksTab}.
 */
import { useCallback, useEffect, useState, type ReactNode } from "react"
import { toast } from "sonner"
import {
  ArrowRightIcon,
  CheckIcon,
  ChevronDownIcon,
  GraduationCapIcon,
  Loader2Icon,
  RefreshCwIcon,
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
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import { NODE_TYPE_LABELS } from "@/components/activity-tree-model"
import {
  ApiRequestError,
  listProgramLinks,
  generateProgramLinks,
  getProgramLinkTargets,
  reviewProgramLink,
  type NodeReviewDecision,
  type ProgramLink,
  type ProgramLinkStatus,
  type ProgramLinkTarget,
} from "@/lib/api/client"

const STATUS_LABEL: Record<ProgramLinkStatus, string> = {
  proposed: "Не проверено",
  approved: "Подтверждено",
  rejected: "Отклонено",
}

const STATUS_CLASS: Record<ProgramLinkStatus, string> = {
  proposed: "bg-muted text-muted-foreground",
  approved: "bg-success/15 text-success",
  rejected: "bg-destructive/15 text-destructive",
}

/** Нейтральная пометка для статуса, которого нет в контракте. */
const FALLBACK_STATUS_LABEL = "Статус неизвестен"

/** Короткая дата решения; неразобранное значение возвращается как есть. */
function formatShortDate(value?: string | null): string {
  if (!value) return ""
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat("ru-RU", {
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date)
}

/** Человекочитаемый тип узла; неизвестный тип показываем как есть. */
function nodeTypeLabel(type: string): string {
  return NODE_TYPE_LABELS[type as keyof typeof NODE_TYPE_LABELS] ?? type
}

/** Понятное сообщение об ошибке загрузки/решения; 501 обрабатывается отдельно. */
function describeLinksError(error: unknown): string {
  if (error instanceof ApiRequestError) {
    if (error.status === 403) return "Недостаточно прав: сопоставления доступны методисту."
    if (error.status === 404) return "Исследование или связь не найдены."
    if (error.status === 0) return "Сервер недоступен: проверьте соединение."
    return error.message
  }
  return "Не удалось получить сопоставления. Попробуйте ещё раз."
}

/** Сворачиваемая группа связей: счётчик в заголовке и состояние раскрытия. */
function LinkGroup({
  title,
  count,
  defaultOpen = true,
  children,
}: {
  title: string
  count: number
  defaultOpen?: boolean
  children: ReactNode
}) {
  const [open, setOpen] = useState(defaultOpen)
  if (count === 0) return null
  return (
    <section className="space-y-2">
      <button
        type="button"
        className="flex w-full items-center gap-2 rounded-lg border bg-muted/20 px-3 py-2 text-left transition-colors hover:bg-muted/40"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <h3 className="text-sm font-medium">
          {title} <span className="text-muted-foreground">({count})</span>
        </h3>
        <ChevronDownIcon
          className={`ml-auto size-4 text-muted-foreground transition-transform ${open ? "rotate-180" : ""}`}
        />
      </button>
      {open && <ul className="space-y-2">{children}</ul>}
    </section>
  )
}

/** Одна связь: узел → элемент программы, объяснение, статус и решение методиста. */
function LinkCard({
  link,
  readOnly,
  busy,
  comment,
  rowError,
  onComment,
  onDecide,
  targets,
  selectedTarget,
  onTargetChange,
}: {
  link: ProgramLink
  readOnly: boolean
  busy: boolean
  comment: string
  rowError?: string | null
  onComment: (value: string) => void
  onDecide: (status: NodeReviewDecision, targetValue:string) => void
  targets: ProgramLinkTarget[]
  selectedTarget:string
  onTargetChange:(value:string)=>void
}) {
  return (
    <li className="space-y-3 rounded-lg border bg-muted/20 p-3">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="grid min-w-0 flex-1 gap-2 sm:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] sm:items-center">
          <div className="min-w-0 space-y-0.5">
            <p className="text-[11px] text-muted-foreground">
              Узел деятельности · {nodeTypeLabel(link.node_type)}
            </p>
            <p className="break-words text-sm font-medium">{link.node_title}</p>
          </div>
          <ArrowRightIcon className="hidden size-4 text-muted-foreground sm:block" aria-hidden />
          <div className="min-w-0 space-y-0.5">
            <p className="text-[11px] text-muted-foreground">Элемент программы</p>
            <p className="break-words text-sm font-medium">{link.competency_title}</p>
            {link.ksa_title && (
              <p className="break-words text-xs text-muted-foreground">{link.ksa_title}</p>
            )}
          </div>
        </div>
        <Badge variant="outline" className={STATUS_CLASS[link.status]}>
          {STATUS_LABEL[link.status] ?? FALLBACK_STATUS_LABEL}
        </Badge>
      </div>

      {link.program_title && (
        <p className="text-[11px] text-muted-foreground">Программа: {link.program_title}</p>
      )}
      <p className="text-xs text-muted-foreground">{link.reason}</p>
      {link.evidence?.length ? (
        <details className="rounded-md border bg-background px-3 py-2 text-sm">
          <summary className="cursor-pointer font-medium">Основания из версии дерева {link.snapshot_version ? `v${link.snapshot_version}` : ""} ({link.evidence.length})</summary>
          <ul className="mt-2 space-y-2">{link.evidence.map((item)=><li key={item.id} className="border-t pt-2"><p className="whitespace-pre-wrap">«{item.quote}»</p><p className="mt-1 text-xs text-muted-foreground">{item.claim_origin==="source_statement"?"утверждение источника":item.claim_origin==="model_interpretation"?"интерпретация модели":"происхождение не классифицировано"}</p></li>)}</ul>
        </details>
      ) : null}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
        <span>{link.source === "system" ? "Предложение системы" : "Решение методиста"}</span>
        {link.reviewed_at && <span>проверено {formatShortDate(link.reviewed_at)}</span>}
      </div>

      {!readOnly && (
        <div className="space-y-2">
          <div className="space-y-1">
            <Label htmlFor={`program-link-target-${link.id}`}>Исправить цель сопоставления</Label>
            <select id={`program-link-target-${link.id}`} value={selectedTarget} onChange={(event)=>onTargetChange(event.target.value)} disabled={busy} className="h-9 w-full rounded-md border bg-background px-3 text-sm">
              {targets.map((target)=><option key={`${target.competency_id}|${target.ksa_id??""}`} value={`${target.competency_id}|${target.ksa_id??""}`}>{target.competency_title}{target.ksa_title?` - ${target.ksa_title}`:" - вся компетенция"}</option>)}
            </select>
          </div>
          <div className="space-y-1">
            <Label htmlFor={`program-link-comment-${link.id}`}>
              Комментарий к решению (необязательно)
            </Label>
            <Textarea
              id={`program-link-comment-${link.id}`}
              value={comment}
              onChange={(event) => onComment(event.target.value)}
              placeholder="Почему связь подтверждена или отклонена"
              className="min-h-12 text-sm"
              disabled={busy}
            />
          </div>
          {rowError && (
            <p className="text-sm text-destructive" role="alert">
              {rowError}
            </p>
          )}
          <div className="flex flex-wrap gap-2">
            {link.status !== "approved" && (
              <Button size="sm" disabled={busy} onClick={() => onDecide("approved", selectedTarget)}>
                {busy ? (
                  <Loader2Icon className="size-3.5 animate-spin" />
                ) : (
                  <CheckIcon className="size-3.5" />
                )}
                Подтвердить
              </Button>
            )}
            {link.status !== "rejected" && (
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() => onDecide("rejected", selectedTarget)}
              >
                <XIcon className="size-3.5" />
                Отклонить
              </Button>
            )}
          </div>
        </div>
      )}
    </li>
  )
}

/** Пустое состояние: связи ещё не сформированы (или сопоставление не включено). */
function EmptyLinks({
  notAvailable,
  refreshing,
  onRefresh,
}: {
  notAvailable: boolean
  refreshing: boolean
  onRefresh: () => void
}) {
  return (
    <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed p-8 text-center">
      <div className="flex size-10 items-center justify-center rounded-full bg-muted">
        <GraduationCapIcon className="size-5 text-muted-foreground" />
      </div>
      <div className="space-y-1">
        <p className="font-medium">Сопоставления ещё не сформированы</p>
        <p className="max-w-md text-sm text-muted-foreground">
          Предложения по связям узлов деятельности с элементами образовательной программы
          строит ядро системы. Когда они появятся, методист сможет подтвердить или отклонить
          каждое предложение.
        </p>
        {notAvailable && (
          <p className="text-sm text-muted-foreground">
            Сопоставление пока не включено: сервер не поддерживает этот раздел.
          </p>
        )}
      </div>
      <Button variant="outline" size="sm" onClick={onRefresh} disabled={refreshing}>
        {refreshing ? (
          <Loader2Icon className="size-3.5 animate-spin" />
        ) : (
          <RefreshCwIcon className="size-3.5" />
        )}
        Обновить
      </Button>
    </div>
  )
}

/**
 * Вкладка «Программа»: список связей с фильтром по узлу (если открыта из дерева)
 * и решение методиста по каждой связи.
 *
 * @param runId Id исследования.
 * @param nodeId Если задан, показываются только связи этого узла; фильтр дублируется
 *   query-параметром `node_id` и клиентской проверкой (бэкенд может не поддерживать его).
 * @param onClearNodeFilter Сброс фильтра по узлу (кнопка «Показать все»).
 * @param readOnly Просмотр без изменения решений.
 */
export function ProgramLinksTab({
  runId,
  nodeId = null,
  onClearNodeFilter,
  readOnly = false,
}: {
  runId: string
  nodeId?: string | null
  onClearNodeFilter?: () => void
  readOnly?: boolean
}) {
  const [links, setLinks] = useState<ProgramLink[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notAvailable, setNotAvailable] = useState(false)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [generating, setGenerating] = useState(false)
  const [targets,setTargets]=useState<ProgramLinkTarget[]>([])
  const [selectedTargets,setSelectedTargets]=useState<Record<string,string>>({})
  const [comments, setComments] = useState<Record<string, string>>({})
  const [rowError, setRowError] = useState<{ id: string; message: string } | null>(null)

  const load = useCallback(
    async (silent = false) => {
      if (silent) setRefreshing(true)
      else setLoading(true)
      try {
        const data = await listProgramLinks(runId, nodeId ?? undefined)
        const rows = Array.isArray(data) ? data : []
        // Фильтр на клиенте страхует случай, когда бэкенд проигнорировал node_id.
        setLinks(nodeId ? rows.filter((row) => row.node_id === nodeId) : rows)
        const programID=rows[0]?.program_id
        if(programID){try{setTargets(await getProgramLinkTargets(programID))}catch{setTargets([])}}
        setError(null)
        setNotAvailable(false)
      } catch (cause) {
        setLinks([])
        if (cause instanceof ApiRequestError && cause.status === 501) {
          setError(null)
          setNotAvailable(true)
        } else {
          setNotAvailable(false)
          setError(describeLinksError(cause))
        }
      } finally {
        setLoading(false)
        setRefreshing(false)
      }
    },
    [nodeId, runId],
  )

  useEffect(() => {
    // Загрузка асинхронная: состояние обновится после ответа API.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load()
  }, [load])

  const decide = async (link: ProgramLink, status: NodeReviewDecision, targetValue:string) => {
    if (busyId) return
    setBusyId(link.id)
    setRowError(null)
    try {
      const [competency_id,ksa_id]=targetValue.split("|")
      const original=`${link.competency_id}|${link.ksa_id??""}`
      const target=targetValue!==original?{competency_id,ksa_id:ksa_id||null}:undefined
      await reviewProgramLink(runId, link.id, status, (comments[link.id] ?? "").trim(),target)
      setComments((previous) => ({ ...previous, [link.id]: "" }))
      toast.success(status === "approved" ? "Связь подтверждена" : "Связь отклонена")
      await load(true)
    } catch (cause) {
      setRowError({ id: link.id, message: describeLinksError(cause) })
    } finally {
      setBusyId(null)
    }
  }

  const generate = async () => {
    if (generating) return
    setGenerating(true)
    try {
      await generateProgramLinks(runId)
      toast.success("Предложения сопоставления подготовлены")
      await load(true)
    } catch (cause) {
      setError(describeLinksError(cause))
      toast.error("Не удалось подготовить сопоставления", { description: describeLinksError(cause) })
    } finally {
      setGenerating(false)
    }
  }

  const proposed = links.filter((link) => link.status === "proposed")
  const approved = links.filter((link) => link.status === "approved")
  const rejected = links.filter((link) => link.status === "rejected")
  const filteredNodeTitle = nodeId
    ? (links.find((link) => link.node_id === nodeId)?.node_title ?? nodeId)
    : null

  return (
    <Card className="gap-4 p-5">
      <CardHeader>
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="space-y-1">
            <CardTitle className="flex items-center gap-2">
              <GraduationCapIcon className="size-4 shrink-0" />
              Сопоставление с программой
            </CardTitle>
            <CardDescription>
              Связи узлов деятельности с элементами образовательной программы. Предложения
              формирует ядро системы, решение по каждой связи принимает методист.
            </CardDescription>
          </div>
          <div className="flex items-center gap-2">
            {!readOnly && <Button variant="outline" size="sm" onClick={() => void generate()} disabled={generating||loading}>{generating?<Loader2Icon className="size-4 animate-spin"/>:<RefreshCwIcon className="size-4"/>}Сформировать предложения</Button>}
            <Button variant="ghost" size="icon-sm" aria-label="Обновить список сопоставлений" disabled={loading || refreshing} onClick={() => void load(true)}>
              <RefreshCwIcon className={`size-4 ${refreshing ? "animate-spin" : ""}`} />
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {nodeId && (
          <div className="flex flex-wrap items-center gap-2 rounded-lg border bg-primary/5 px-3 py-2 text-xs">
            <span className="min-w-0">
              Показаны сопоставления узла <span className="font-medium">{filteredNodeTitle}</span>
            </span>
            {onClearNodeFilter && (
              <Button
                variant="ghost"
                size="sm"
                className="ml-auto h-6 px-2 text-xs"
                onClick={onClearNodeFilter}
              >
                Показать все
              </Button>
            )}
          </div>
        )}

        {loading ? (
          <div className="space-y-2">
            <Skeleton className="h-24 w-full" />
            <Skeleton className="h-24 w-full" />
          </div>
        ) : error ? (
          <div className="space-y-3 rounded-lg border border-destructive/50 p-4 text-sm">
            <p className="text-destructive">{error}</p>
            <Button variant="outline" size="sm" onClick={() => void load()}>
              Повторить
            </Button>
          </div>
        ) : links.length === 0 ? (
          <EmptyLinks
            notAvailable={notAvailable}
            refreshing={refreshing}
            onRefresh={() => void load(true)}
          />
        ) : (
          <>
            <p className="text-xs text-muted-foreground">
              Всего связей: {links.length} · подтверждено {approved.length} · отклонено{" "}
              {rejected.length} · не проверено {proposed.length}
            </p>
            {notAvailable && (
              <p className="rounded-lg border bg-muted/20 p-3 text-sm text-muted-foreground">
                Сопоставление пока не включено: сервер не поддерживает этот раздел.
              </p>
            )}
            <LinkGroup title="Не проверено" count={proposed.length}>
              {proposed.map((link) => (
                <LinkCard
                  key={link.id}
                  link={link}
                  readOnly={readOnly}
                  busy={busyId === link.id}
                  comment={comments[link.id] ?? ""}
                  rowError={rowError?.id === link.id ? rowError.message : null}
                  onComment={(value) =>
                    setComments((previous) => ({ ...previous, [link.id]: value }))
                  }
                  onDecide={(status,target) => void decide(link, status,target)}
                  targets={targets}
                  selectedTarget={selectedTargets[link.id]??`${link.competency_id}|${link.ksa_id??""}`}
                  onTargetChange={(value)=>setSelectedTargets(old=>({...old,[link.id]:value}))}
                />
              ))}
            </LinkGroup>
            <LinkGroup title="Подтверждено" count={approved.length}>
              {approved.map((link) => (
                <LinkCard
                  key={link.id}
                  link={link}
                  readOnly={readOnly}
                  busy={busyId === link.id}
                  comment={comments[link.id] ?? ""}
                  rowError={rowError?.id === link.id ? rowError.message : null}
                  onComment={(value) =>
                    setComments((previous) => ({ ...previous, [link.id]: value }))
                  }
                  onDecide={(status,target) => void decide(link, status,target)}
                  targets={targets}
                  selectedTarget={selectedTargets[link.id]??`${link.competency_id}|${link.ksa_id??""}`}
                  onTargetChange={(value)=>setSelectedTargets(old=>({...old,[link.id]:value}))}
                />
              ))}
            </LinkGroup>
            <LinkGroup title="Отклонённые" count={rejected.length} defaultOpen={false}>
              {rejected.map((link) => (
                <LinkCard
                  key={link.id}
                  link={link}
                  readOnly={readOnly}
                  busy={busyId === link.id}
                  comment={comments[link.id] ?? ""}
                  rowError={rowError?.id === link.id ? rowError.message : null}
                  onComment={(value) =>
                    setComments((previous) => ({ ...previous, [link.id]: value }))
                  }
                  onDecide={(status,target) => void decide(link, status,target)}
                  targets={targets}
                  selectedTarget={selectedTargets[link.id]??`${link.competency_id}|${link.ksa_id??""}`}
                  onTargetChange={(value)=>setSelectedTargets(old=>({...old,[link.id]:value}))}
                />
              ))}
            </LinkGroup>
          </>
        )}
      </CardContent>
    </Card>
  )
}
