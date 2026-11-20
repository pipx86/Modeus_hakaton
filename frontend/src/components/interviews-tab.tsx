/**
 * @file interviews-tab.tsx
 * @description Вкладка «Интервью»: история интервью исследования и сообщения выбранного
 * интервью.
 *
 * Данные: `GET /runs/{id}/interviews` (список с респондентом и статусом) и
 * `GET /runs/{id}/interviews/{interview_id}/messages` (история сообщений).
 * Список и история - отдельные запросы: при переключении интервью сообщения сначала
 * очищаются, а «поздний» ответ предыдущего интервью игнорируется по номеру запроса,
 * поэтому истории разных интервью не смешиваются. Если API вернул ошибку, экран
 * показывает её и кнопку повтора, а не пустой список.
 *
 * Экспорт: {@link InterviewsTab}.
 */
import { useCallback, useEffect, useRef, useState } from "react"
import { MessagesSquareIcon, RefreshCwIcon, UserIcon } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  ApiRequestError,
  listInterviewMessages,
  listRunInterviews,
  type InterviewMessage,
  type InterviewStatus,
  type RunInterview,
} from "@/lib/api/client"

const INTERVIEW_STATUS_LABEL: Record<InterviewStatus, string> = {
  active: "Идёт",
  completed: "Завершено",
  failed: "Прервано",
}

const INTERVIEW_STATUS_CLASS: Record<InterviewStatus, string> = {
  active: "bg-primary/10 text-primary",
  completed: "bg-success/15 text-success",
  failed: "bg-destructive/15 text-destructive",
}

/** Короткая дата и время; неразобранное значение возвращается как есть. */
function formatDateTime(value?: string | null): string {
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

/** Понятное сообщение об ошибке списка или истории интервью. */
function describeInterviewsError(error: unknown): string {
  if (error instanceof ApiRequestError) {
    if (error.status === 403) return "Недостаточно прав для просмотра истории интервью."
    if (error.status === 404) return "Интервью не найдено."
    if (error.status === 501) return "История интервью пока не включена на сервере."
    if (error.status === 0) return "Сервер недоступен: история интервью не загружена."
    return error.message
  }
  return "Не удалось загрузить историю интервью."
}

/** Подпись респондента: имя, роль или нейтральная заглушка. */
function respondentLabel(interview: RunInterview): string {
  const name = interview.respondent_name?.trim()
  if (name) return name
  return "Респондент не указан"
}

/**
 * Вкладка «Интервью»: слева список интервью, справа история выбранного.
 *
 * @param runId Id исследования.
 */
export function InterviewsTab({ runId }: { runId: string }) {
  const [interviews, setInterviews] = useState<RunInterview[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [messages, setMessages] = useState<InterviewMessage[]>([])
  const [messagesLoading, setMessagesLoading] = useState(false)
  const [messagesError, setMessagesError] = useState<string | null>(null)
  const [messagesToken, setMessagesToken] = useState(0)
  /** Номер последнего запроса истории: ответы прошлых интервью не применяются. */
  const messagesSequence = useRef(0)

  const loadInterviews = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listRunInterviews(runId)
      const rows = Array.isArray(data) ? data : []
      setInterviews(rows)
      setError(null)
      setSelectedId((previous) =>
        previous && rows.some((interview) => interview.id === previous)
          ? previous
          : (rows[0]?.id ?? null),
      )
    } catch (cause) {
      setInterviews([])
      setSelectedId(null)
      setError(describeInterviewsError(cause))
    } finally {
      setLoading(false)
    }
  }, [runId])

  useEffect(() => {
    // Загрузка асинхронная: состояние обновится после ответа API.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void loadInterviews()
  }, [loadInterviews])

  const loadMessages = useCallback(
    async (interviewId: string | null) => {
      const sequence = ++messagesSequence.current
      // Очищаем историю до запроса: сообщения разных интервью не смешиваются.
      setMessages([])
      setMessagesError(null)
      if (!interviewId) {
        setMessagesLoading(false)
        return
      }
      setMessagesLoading(true)
      try {
        const data = await listInterviewMessages(runId, interviewId)
        if (sequence !== messagesSequence.current) return
        setMessages(Array.isArray(data) ? data : [])
      } catch (cause) {
        if (sequence !== messagesSequence.current) return
        setMessages([])
        setMessagesError(describeInterviewsError(cause))
      } finally {
        if (sequence === messagesSequence.current) setMessagesLoading(false)
      }
    },
    [runId],
  )

  useEffect(() => {
    // Загрузка асинхронная; при смене интервью «поздний» ответ инвалидируется.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void loadMessages(selectedId)
    return () => {
      messagesSequence.current += 1
    }
  }, [loadMessages, selectedId, messagesToken])

  const selected = interviews.find((interview) => interview.id === selectedId) ?? null

  return (
    <div className="grid gap-4 lg:grid-cols-[300px_minmax(0,1fr)]">
      <section className="space-y-2 rounded-xl border p-3" aria-label="Интервью исследования">
        <div className="flex items-center justify-between gap-2">
          <p className="text-sm font-medium">Интервью</p>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Обновить список интервью"
            disabled={loading}
            onClick={() => void loadInterviews()}
          >
            <RefreshCwIcon className="size-4" />
          </Button>
        </div>

        {loading ? (
          <div className="space-y-2">
            <Skeleton className="h-14 w-full" />
            <Skeleton className="h-14 w-full" />
          </div>
        ) : error ? (
          <div className="space-y-2 rounded-lg border border-destructive/50 p-3 text-sm">
            <p className="text-destructive">{error}</p>
            <Button variant="outline" size="sm" onClick={() => void loadInterviews()}>
              Повторить
            </Button>
          </div>
        ) : interviews.length === 0 ? (
          <div className="space-y-1 rounded-lg border border-dashed p-4 text-center">
            <p className="text-sm font-medium">Интервью пока нет</p>
            <p className="text-xs text-muted-foreground">
              История появится, когда начнётся интервью с респондентом.
            </p>
          </div>
        ) : (
          <ul className="space-y-1.5">
            {interviews.map((interview) => {
              const active = interview.id === selectedId
              return (
                <li key={interview.id}>
                  <button
                    type="button"
                    aria-pressed={active}
                    aria-label={`История интервью: ${respondentLabel(interview)}`}
                    onClick={() => setSelectedId(interview.id)}
                    className={`w-full space-y-1 rounded-lg border px-3 py-2 text-left transition-colors ${
                      active ? "border-primary/50 bg-primary/5" : "hover:bg-muted/40"
                    }`}
                  >
                    <span className="flex items-center gap-2">
                      <UserIcon className="size-3.5 shrink-0 text-muted-foreground" />
                      <span className="min-w-0 flex-1 truncate text-sm font-medium">
                        {respondentLabel(interview)}
                      </span>
                      <Badge
                        variant="outline"
                        className={INTERVIEW_STATUS_CLASS[interview.status]}
                      >
                        {INTERVIEW_STATUS_LABEL[interview.status] ?? interview.status}
                      </Badge>
                    </span>
                    {interview.respondent_role && (
                      <span className="block truncate text-xs text-muted-foreground">
                        {interview.respondent_role}
                      </span>
                    )}
                    <span className="block text-[11px] text-muted-foreground">
                      {formatDateTime(interview.started_at)}
                      {interview.finished_at ? ` - ${formatDateTime(interview.finished_at)}` : ""}
                    </span>
                  </button>
                </li>
              )
            })}
          </ul>
        )}
      </section>

      <section className="space-y-3 rounded-xl border p-4" aria-label="История выбранного интервью">
        {selected ? (
          <>
            <div className="flex flex-wrap items-start justify-between gap-2">
              <div className="space-y-0.5">
                <p className="text-sm font-medium">{respondentLabel(selected)}</p>
                <p className="text-xs text-muted-foreground">
                  {selected.respondent_role ? `${selected.respondent_role} · ` : ""}
                  Начато {formatDateTime(selected.started_at)}
                  {selected.finished_at ? ` · завершено ${formatDateTime(selected.finished_at)}` : ""}
                </p>
              </div>
              <div className="flex items-center gap-2">
                <Badge variant="outline" className={INTERVIEW_STATUS_CLASS[selected.status]}>
                  {INTERVIEW_STATUS_LABEL[selected.status] ?? selected.status}
                </Badge>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label="Обновить историю интервью"
                  disabled={messagesLoading}
                  onClick={() => setMessagesToken((token) => token + 1)}
                >
                  <RefreshCwIcon className="size-4" />
                </Button>
              </div>
            </div>

            {messagesLoading ? (
              <div className="space-y-2">
                <Skeleton className="h-16 w-3/4" />
                <Skeleton className="h-16 w-3/4 self-end" />
              </div>
            ) : messagesError ? (
              <div className="space-y-2 rounded-lg border border-destructive/50 p-3 text-sm">
                <p className="text-destructive">{messagesError}</p>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setMessagesToken((token) => token + 1)}
                >
                  Повторить
                </Button>
              </div>
            ) : messages.length === 0 ? (
              <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed p-8 text-center">
                <MessagesSquareIcon className="size-5 text-muted-foreground" />
                <p className="text-sm text-muted-foreground">В этом интервью пока нет сообщений.</p>
              </div>
            ) : (
              <ul className="max-h-[520px] space-y-2 overflow-y-auto pr-1">
                {messages.map((message) => {
                  const fromUser = message.role === "user"
                  const citations = (message.citations ?? []).filter(
                    (citation) => citation && typeof citation.quote === "string" && citation.quote.trim(),
                  )
                  return (
                    <li
                      key={message.id}
                      className={`space-y-1 rounded-lg border p-3 text-sm ${
                        fromUser ? "border-primary/30 bg-primary/5" : "bg-muted/30"
                      }`}
                    >
                      <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
                        <span className="font-medium text-foreground">
                          {fromUser ? "Респондент" : "Интервьюер"}
                        </span>
                        <span>{formatDateTime(message.created_at)}</span>
                      </div>
                      <p className="whitespace-pre-wrap break-words leading-relaxed">
                        {message.content}
                      </p>
                      {citations.length > 0 && (
                        <ul className="space-y-1">
                          {citations.map((citation, index) => (
                            <li
                              key={citation.id ?? citation.ref_id ?? `citation-${index}`}
                              className="rounded-md border bg-background/70 px-2 py-1 text-xs text-muted-foreground"
                            >
                              {citation.title ? `${citation.title}: ` : ""}«{citation.quote}»
                            </li>
                          ))}
                        </ul>
                      )}
                    </li>
                  )
                })}
              </ul>
            )}
          </>
        ) : (
          <div className="flex min-h-40 flex-col items-center justify-center gap-2 text-center">
            <MessagesSquareIcon className="size-5 text-muted-foreground" />
            <p className="text-sm text-muted-foreground">
              Выберите интервью слева, чтобы посмотреть историю сообщений.
            </p>
          </div>
        )}
      </section>
    </div>
  )
}
