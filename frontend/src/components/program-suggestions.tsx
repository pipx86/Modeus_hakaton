/**
 * @file program-suggestions.tsx
 * @description Предложения по учебным изменениям на основе сопоставления
 * деятельности с образовательной программой: `GET /runs/{id}/program-suggestions`.
 *
 * Секция вкладки «Программа». Система показывает кандидатов на рассмотрение
 * (несопоставленные задачи и действия, неподтверждённые и несвязанные
 * компетенции, противоречивые связи), но ничего не утверждает и не переписывает
 * программу: статус каждого предложения - «proposal», решение принимает
 * методист. Формулировки нейтральные, без оценок программы.
 *
 * Пустой список предложений - нормальный ответ (200): показываются summary и
 * спокойное пустое состояние. 501 - «раздел пока не поддерживается сервером»,
 * 4xx/5xx - видимое сообщение с кнопкой повтора.
 *
 * Экспорт: {@link ProgramSuggestions}.
 */
import { useEffect, useState } from "react"
import { RefreshCwIcon, SparklesIcon } from "lucide-react"
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
import {
  ApiRequestError,
  getProgramSuggestions,
  type ProgramProposal,
  type ProgramProposalKind,
  type ProgramProposalPriority,
  type ProgramSuggestions as ProgramSuggestionsResponse,
} from "@/lib/api/client"

const PROPOSAL_KIND_LABELS: Record<ProgramProposalKind, string> = {
  unmapped_activity: "Элемент деятельности без подтверждённой связи",
  unconfirmed_competency: "Неподтверждённая компетенция",
  uncovered_competency: "Компетенция без связей с деятельностью",
  conflicting_links: "Противоречивые связи",
}

const PRIORITY_LABELS: Record<ProgramProposalPriority, string> = {
  high: "Высокий приоритет",
  medium: "Средний приоритет",
  low: "Низкий приоритет",
}

const PRIORITY_CLASSES: Record<ProgramProposalPriority, string> = {
  high: "bg-warning/15 text-warning",
  medium: "bg-primary/10 text-primary",
  low: "bg-muted text-muted-foreground",
}

/** Понятное сообщение об ошибке загрузки; 501 обрабатывается отдельно. */
function describeSuggestionsError(error: unknown): string {
  if (error instanceof ApiRequestError) {
    if (error.status === 403) {
      return "Доступ к предложениям есть только у владельца исследования и администратора."
    }
    if (error.status === 404) return "Исследование не найдено."
    if (error.status === 0) return "Сервер недоступен: проверьте соединение."
    return error.message
  }
  if (error && typeof error === "object") {
    const message = (error as { message?: unknown }).message
    if (typeof message === "string" && message.trim()) return message
  }
  return "Не удалось загрузить предложения по программе."
}

/** Одно предложение: приоритет, вид, объяснение и цель. */
function ProposalCard({ proposal }: { proposal: ProgramProposal }) {
  const priority = proposal.priority in PRIORITY_LABELS ? proposal.priority : "low"
  const kindLabel = PROPOSAL_KIND_LABELS[proposal.kind] ?? proposal.kind
  const target = proposal.target
  return (
    <li className="space-y-2 rounded-lg border bg-muted/20 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline" className={PRIORITY_CLASSES[priority]}>
          {PRIORITY_LABELS[priority]}
        </Badge>
        <Badge variant="secondary">{kindLabel}</Badge>
        <Badge variant="outline" className="ml-auto">
          Предложение
        </Badge>
      </div>
      <p className="text-sm">{proposal.rationale}</p>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
        {target.competency_title && (
          <span>
            Компетенция: <span className="text-foreground">{target.competency_title}</span>
          </span>
        )}
      </div>
    </li>
  )
}

/**
 * Предложения по программе для исследования.
 *
 * @param runId Id исследования.
 */
export function ProgramSuggestions({ runId }: { runId: string }) {
  const [data, setData] = useState<ProgramSuggestionsResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notAvailable, setNotAvailable] = useState(false)
  const [reloadToken, setReloadToken] = useState(0)

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      setLoading(true)
      try {
        const response = await getProgramSuggestions(runId)
        if (cancelled) return
        setData(response)
        setError(null)
        setNotAvailable(false)
      } catch (cause) {
        if (cancelled) return
        setData(null)
        if (cause instanceof ApiRequestError && cause.status === 501) {
          setError(null)
          setNotAvailable(true)
        } else {
          setNotAvailable(false)
          setError(describeSuggestionsError(cause))
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

  const proposals = data?.proposals ?? []
  const high = proposals.filter((proposal) => proposal.priority === "high").length
  const medium = proposals.filter((proposal) => proposal.priority === "medium").length
  const low = proposals.filter((proposal) => proposal.priority === "low").length

  return (
    <Card className="gap-4 p-5">
      <CardHeader>
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="space-y-1">
            <CardTitle className="flex items-center gap-2">
              <SparklesIcon className="size-4 shrink-0" />
              Предложения по программе
            </CardTitle>
            <CardDescription>
              Кандидаты на рассмотрение по итогам сопоставления деятельности с
              программой. Система только предлагает: она не утверждает связи и не
              переписывает программу, решение принимает методист.
            </CardDescription>
          </div>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Обновить предложения по программе"
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
              Повторить загрузку предложений
            </Button>
          </div>
        ) : notAvailable ? (
          <div className="space-y-1 rounded-lg border border-dashed p-6 text-center">
            <p className="font-medium">Предложения пока недоступны</p>
            <p className="text-sm text-muted-foreground">
              Раздел пока не поддерживается сервером.
            </p>
          </div>
        ) : !data ? null : (
          <>
            {data.program_title && (
              <p className="text-sm text-muted-foreground">
                Программа:{" "}
                <span className="font-medium text-foreground">{data.program_title}</span>
              </p>
            )}

            {proposals.length === 0 ? (
              <div className="space-y-1 rounded-lg border border-dashed p-6 text-center">
                <p className="font-medium">Предложений нет</p>
                <p className="text-sm text-muted-foreground">
                  {data.summary ||
                    "Открытых кандидатов на рассмотрение не обнаружено."}
                </p>
              </div>
            ) : (
              <>
                <div className="flex flex-wrap items-center gap-2" aria-label="Счётчики предложений">
                  <Badge variant="outline" className={PRIORITY_CLASSES.high}>
                    Высокий: {high}
                  </Badge>
                  <Badge variant="outline" className={PRIORITY_CLASSES.medium}>
                    Средний: {medium}
                  </Badge>
                  <Badge variant="outline" className={PRIORITY_CLASSES.low}>
                    Низкий: {low}
                  </Badge>
                  <Badge variant="outline">Всего: {proposals.length}</Badge>
                </div>
                {data.summary && (
                  <p className="text-sm text-muted-foreground">{data.summary}</p>
                )}
                <ul className="space-y-2" aria-label="Предложения по программе">
                  {proposals.map((proposal) => (
                    <ProposalCard key={proposal.id} proposal={proposal} />
                  ))}
                </ul>
              </>
            )}

            {(data.disclaimer || proposals.length > 0) && (
              <p className="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">
                {data.disclaimer ||
                  "Предложения системы, решение принимает методист; система не утверждает и не переписывает программу."}
              </p>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}
