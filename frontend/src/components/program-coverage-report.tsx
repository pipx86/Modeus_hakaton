/**
 * @file program-coverage-report.tsx
 * @description Отчёт о покрытии образовательной программы деятельностью:
 * `GET /runs/{id}/program-coverage`.
 *
 * Показывает счётчики, узлы с подтверждёнными связями, узлы только с
 * предложенными связями, узлы без связей, компетенции программы, связанные с
 * деятельностью, и отдельно предложения, ожидающие решения. Формулировки
 * нейтральные: отсутствие связи - «не сопоставлено», а не «пробел» или
 * «недостаток программы».
 *
 * Ответ 501 - нейтральное пустое состояние (раздел ещё не включён на сервере),
 * прочие 4xx/5xx - видимое сообщение с кнопкой повтора.
 *
 * Экспорт: {@link ProgramCoverageReport}.
 */
import { useEffect, useState, type ReactNode } from "react"
import {
  ChevronDownIcon,
  GraduationCapIcon,
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
import { Skeleton } from "@/components/ui/skeleton"
import { NODE_TYPE_LABELS } from "@/components/activity-tree-model"
import {
  ApiRequestError,
  getProgramCoverage,
  type ProgramCoverage,
  type ProgramCoverageLink,
  type ProgramCoverageNode,
  type ProgramCoverageStatus,
  type ProgramLinkStatus,
} from "@/lib/api/client"

const COVERAGE_STATUS_LABEL: Record<ProgramCoverageStatus, string> = {
  confirmed: "Подтверждено",
  proposed: "Предложено",
  none: "Не сопоставлено",
}

const COVERAGE_STATUS_CLASS: Record<ProgramCoverageStatus, string> = {
  confirmed: "bg-success/15 text-success",
  proposed: "bg-warning/15 text-warning",
  none: "bg-muted text-muted-foreground",
}

const LINK_STATUS_LABEL: Record<ProgramLinkStatus, string> = {
  proposed: "Предложено",
  approved: "Подтверждено",
  rejected: "Отклонено",
}

const LINK_STATUS_CLASS: Record<ProgramLinkStatus, string> = {
  proposed: "bg-warning/15 text-warning",
  approved: "bg-success/15 text-success",
  rejected: "bg-destructive/15 text-destructive",
}

/** Человекочитаемый тип узла; неизвестный тип показываем как есть. */
function nodeTypeLabel(type: string): string {
  return NODE_TYPE_LABELS[type as keyof typeof NODE_TYPE_LABELS] ?? type
}

/** Понятное сообщение об ошибке загрузки; 501 обрабатывается отдельно. */
function describeCoverageError(error: unknown): string {
  if (error instanceof ApiRequestError) {
    if (error.status === 403) {
      return "Недостаточно прав: отчёт о покрытии доступен методисту."
    }
    if (error.status === 404) return "Исследование или программа не найдены."
    if (error.status === 0) return "Сервер недоступен: проверьте соединение."
    return error.message
  }
  if (error && typeof error === "object") {
    const message = (error as { message?: unknown }).message
    if (typeof message === "string" && message.trim()) return message
  }
  return "Не удалось загрузить отчёт о покрытии программы."
}

/** Сворачиваемая группа узлов отчёта: заголовок со счётчиком и состояние раскрытия. */
function CoverageGroup({
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

/** Один узел отчёта: тип, название, статус покрытия и связи с компетенциями. */
function CoverageNodeCard({ node }: { node: ProgramCoverageNode }) {
  const status = node.status in COVERAGE_STATUS_LABEL ? node.status : "none"
  return (
    <li className="space-y-2 rounded-lg border bg-muted/20 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="secondary">{nodeTypeLabel(node.node_type)}</Badge>
        <span className="min-w-0 break-words text-sm font-medium">{node.node_title}</span>
        <Badge variant="outline" className={`ml-auto ${COVERAGE_STATUS_CLASS[status]}`}>
          {COVERAGE_STATUS_LABEL[status]}
        </Badge>
        <code className="w-full text-[11px] text-muted-foreground sm:w-auto">{node.node_id}</code>
      </div>
      {node.links.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          Связь не установлена: узел не сопоставлен с элементами программы.
        </p>
      ) : (
        <ul className="space-y-1.5">
          {node.links.map((link) => (
            <li
              key={link.link_id}
              className="rounded-md border bg-background/60 px-2.5 py-1.5 text-sm"
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className="min-w-0 flex-1 break-words">{link.competency_title}</span>
                <Badge
                  variant="outline"
                  className={LINK_STATUS_CLASS[link.status] ?? "bg-muted text-muted-foreground"}
                >
                  {LINK_STATUS_LABEL[link.status] ?? link.status}
                </Badge>
              </div>
              {link.reason && (
                <p className="mt-1 text-xs text-muted-foreground">{link.reason}</p>
              )}
            </li>
          ))}
        </ul>
      )}
    </li>
  )
}

/** Строка «предложение, ожидающее решения»: узел → компетенция и причина. */
function ProposedLinkRow({
  link,
  node,
}: {
  link: ProgramCoverageLink
  node: ProgramCoverageNode
}) {
  return (
    <li className="space-y-1 rounded-lg border bg-muted/20 p-3 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 break-words font-medium">{node.node_title}</span>
        <span className="text-muted-foreground">→</span>
        <span className="min-w-0 break-words">{link.competency_title}</span>
        <Badge variant="outline" className="ml-auto bg-warning/15 text-warning">
          {LINK_STATUS_LABEL[link.status] ?? link.status}
        </Badge>
      </div>
      {link.reason && <p className="text-xs text-muted-foreground">{link.reason}</p>}
    </li>
  )
}

/**
 * Отчёт о покрытии программы деятельностью.
 *
 * @param runId Id исследования.
 */
export function ProgramCoverageReport({ runId }: { runId: string }) {
  const [coverage, setCoverage] = useState<ProgramCoverage | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notAvailable, setNotAvailable] = useState(false)
  const [reloadToken, setReloadToken] = useState(0)

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      setLoading(true)
      try {
        const data = await getProgramCoverage(runId)
        if (cancelled) return
        setCoverage(data)
        setError(null)
        setNotAvailable(false)
      } catch (cause) {
        if (cancelled) return
        setCoverage(null)
        if (cause instanceof ApiRequestError && cause.status === 501) {
          setError(null)
          setNotAvailable(true)
        } else {
          setNotAvailable(false)
          setError(describeCoverageError(cause))
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

  const confirmedNodes = (coverage?.nodes ?? []).filter((node) => node.status === "confirmed")
  const proposedNodes = (coverage?.nodes ?? []).filter((node) => node.status === "proposed")
  const noneNodes = (coverage?.nodes ?? []).filter(
    (node) => node.status !== "confirmed" && node.status !== "proposed",
  )
  const proposedLinks = (coverage?.nodes ?? []).flatMap((node) =>
    node.links
      .filter((link) => link.status === "proposed")
      .map((link) => ({ link, node })),
  )
  const linkedCompetencies = (coverage?.competencies ?? []).filter(
    (competency) => competency.links_count > 0,
  )
  const counts = coverage?.counts

  return (
    <Card className="gap-4 p-5">
      <CardHeader>
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="space-y-1">
            <CardTitle className="flex items-center gap-2">
              <GraduationCapIcon className="size-4 shrink-0" />
              Отчёт о покрытии программы деятельностью
            </CardTitle>
            <CardDescription>
              Сопоставление узлов деятельности с элементами образовательной программы.
              Отсутствие связи означает только «не сопоставлено» - это констатация текущего
              состояния, а не оценка программы.
            </CardDescription>
          </div>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Обновить отчёт о покрытии"
            disabled={loading}
            onClick={() => setReloadToken((token) => token + 1)}
          >
            <RefreshCwIcon className={`size-4 ${loading ? "animate-spin" : ""}`} />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {loading ? (
          <div className="space-y-2">
            <Skeleton className="h-8 w-2/3" />
            <Skeleton className="h-24 w-full" />
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
        ) : !coverage ? (
          <div className="space-y-1 rounded-lg border border-dashed p-6 text-center">
            <p className="font-medium">Отчёт о покрытии пока не сформирован</p>
            <p className="text-sm text-muted-foreground">
              Отчёт появится, когда система сопоставит узлы деятельности с элементами
              образовательной программы.
            </p>
            {notAvailable && (
              <p className="text-sm text-muted-foreground">
                Раздел пока не поддерживается сервером.
              </p>
            )}
          </div>
        ) : (
          <>
            <div className="space-y-2">
              {coverage.program_title && (
                <p className="text-sm text-muted-foreground">
                  Программа: <span className="font-medium text-foreground">{coverage.program_title}</span>
                </p>
              )}
              <div className="flex flex-wrap items-center gap-2" aria-label="Счётчики покрытия">
                <Badge variant="outline" className="bg-success/15 text-success">
                  Подтверждено: {counts?.confirmed ?? 0}
                </Badge>
                <Badge variant="outline" className="bg-warning/15 text-warning">
                  Предложено: {counts?.proposed ?? 0}
                </Badge>
                <Badge variant="outline" className="bg-muted text-muted-foreground">
                  Не сопоставлено: {counts?.none ?? 0}
                </Badge>
                <Badge variant="outline">Всего узлов: {counts?.total ?? coverage.nodes.length}</Badge>
              </div>
            </div>

            {coverage.nodes.length === 0 ? (
              <div className="space-y-1 rounded-lg border border-dashed p-6 text-center">
                <p className="font-medium">Отчёт о покрытии пока не сформирован</p>
                <p className="text-sm text-muted-foreground">
                  В исследовании ещё нет узлов деятельности или сопоставлений с программой.
                </p>
                {notAvailable && (
                  <p className="text-sm text-muted-foreground">
                    Раздел пока не поддерживается сервером.
                  </p>
                )}
              </div>
            ) : (
              <div className="space-y-3">
                <CoverageGroup title="Подтверждённые связи" count={confirmedNodes.length}>
                  {confirmedNodes.map((node) => (
                    <CoverageNodeCard key={node.node_id} node={node} />
                  ))}
                </CoverageGroup>
                <CoverageGroup title="Только предложенные связи" count={proposedNodes.length}>
                  {proposedNodes.map((node) => (
                    <CoverageNodeCard key={node.node_id} node={node} />
                  ))}
                </CoverageGroup>
                <CoverageGroup
                  title="Не сопоставлено"
                  count={noneNodes.length}
                  defaultOpen={false}
                >
                  {noneNodes.map((node) => (
                    <CoverageNodeCard key={node.node_id} node={node} />
                  ))}
                </CoverageGroup>
              </div>
            )}

            <section className="space-y-2">
              <h3 className="text-sm font-medium">
                Компетенции программы, связанные с деятельностью
              </h3>
              {linkedCompetencies.length === 0 ? (
                <p className="rounded-lg border bg-muted/20 p-3 text-sm text-muted-foreground">
                  Связанных компетенций пока нет.
                </p>
              ) : (
                <ul className="space-y-1.5">
                  {linkedCompetencies.map((competency) => (
                    <li
                      key={competency.competency_id}
                      className="flex flex-wrap items-center gap-2 rounded-lg border bg-muted/20 px-3 py-2 text-sm"
                    >
                      <span className="min-w-0 flex-1 break-words">
                        {competency.competency_title}
                      </span>
                      <Badge variant="outline">Связей: {competency.links_count}</Badge>
                    </li>
                  ))}
                </ul>
              )}
            </section>

            <section className="space-y-2" aria-label="Предложения, ожидающие решения">
              <h3 className="text-sm font-medium">
                Предложения, ожидающие решения{" "}
                <span className="text-muted-foreground">({proposedLinks.length})</span>
              </h3>
              {proposedLinks.length === 0 ? (
                <p className="rounded-lg border bg-muted/20 p-3 text-sm text-muted-foreground">
                  Предложений, ожидающих решения, нет.
                </p>
              ) : (
                <ul className="space-y-2">
                  {proposedLinks.map(({ link, node }) => (
                    <ProposedLinkRow key={link.link_id} link={link} node={node} />
                  ))}
                </ul>
              )}
            </section>
          </>
        )}
      </CardContent>
    </Card>
  )
}
