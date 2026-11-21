/**
 * @file interviews-tab.tsx
 * @description Вкладка «Интервью»: история интервью исследования, сообщения выбранного
 * интервью и основания, которые его ответы дали дереву деятельности.
 *
 * Данные: `GET /runs/{id}/interviews` (список с респондентом и статусом),
 * `GET /runs/{id}/interviews/{interview_id}/messages` (история сообщений) и
 * `GET /runs/{id}/interviews/{interview_id}/evidence` (цитаты, узлы и статусы
 * обоснований). Три запроса независимы, а при переключении интервью их состояние
 * сначала очищается, а «поздний» ответ предыдущего интервью игнорируется по номеру
 * запроса - поэтому истории и основания разных интервью не смешиваются. Если API
 * вернул ошибку, экран показывает её и кнопку повтора, а не пустой список; блок
 * оснований при 501/ошибке остаётся нейтральным («Основания ещё не связаны с
 * ответами») и не роняет вкладку.
 *
 * Экспорт: {@link InterviewsTab}.
 */
import { useCallback, useEffect, useRef, useState } from "react"
import { MessagesSquareIcon, RefreshCwIcon, UserIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { NODE_TYPE_LABELS } from "@/components/activity-tree-model"
import {
  ApiRequestError,
  listInterviewEvidence,
  listInterviewMessages,
  listRunInterviews,
  startRunInterview,
  type EvidenceStatus,
  type InterviewEvidence,
  type InterviewMessage,
  type RunInterview,
} from "@/lib/api/client"



const EVIDENCE_STATUS_LABEL: Record<EvidenceStatus, string> = {
  pending: "На проверке",
  approved: "Подтверждено",
  rejected: "Отклонено",
}

const EVIDENCE_STATUS_CLASS: Record<EvidenceStatus, string> = {
  pending: "bg-muted text-muted-foreground",
  approved: "bg-success/15 text-success",
  rejected: "bg-destructive/15 text-destructive",
}

/** Человекочитаемый тип узла; неизвестный тип показываем как есть. */
function nodeTypeLabel(type: string): string {
  return NODE_TYPE_LABELS[type as keyof typeof NODE_TYPE_LABELS] ?? type
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
 * Вкладка «Интервью»: слева список интервью, справа история выбранного и
 * основания, которые его ответы дали дереву деятельности.
 *
 * @param runId Id исследования.
 * @param onFocusNode Переход к узлу на вкладке «Дерево»: используется кнопкой
 *   узла в блоке «Ответы, ставшие основаниями». Без колбэка узел показан текстом.
 */
export function InterviewsTab({
  runId,
  onFocusNode,
  canStart = false,
  onStarted,
}: {
  runId: string
  onFocusNode?: (nodeId: string) => void
  canStart?: boolean
  onStarted?: () => void
}) {
  const [showStart, setShowStart] = useState(false)
  const [name, setName] = useState("")
  const [role, setRole] = useState("")
  const [starting, setStarting] = useState(false)
  const [startError, setStartError] = useState<string | null>(null)
  const [interviews, setInterviews] = useState<RunInterview[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [messages, setMessages] = useState<InterviewMessage[]>([])
  const [messagesLoading, setMessagesLoading] = useState(false)
  const [messagesError, setMessagesError] = useState<string | null>(null)
  const [messagesToken, setMessagesToken] = useState(0)
  const [evidence, setEvidence] = useState<InterviewEvidence[]>([])
  const [evidenceLoading, setEvidenceLoading] = useState(false)
  const [evidenceError, setEvidenceError] = useState<string | null>(null)
  const [evidenceNotAvailable, setEvidenceNotAvailable] = useState(false)
  const [evidenceToken, setEvidenceToken] = useState(0)
  /** Номер последнего запроса истории: ответы прошлых интервью не применяются. */
  const messagesSequence = useRef(0)
  /** Номер последнего запроса оснований: основания прошлых интервью не смешиваются. */
  const evidenceSequence = useRef(0)

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

  const loadEvidence = useCallback(
    async (interviewId: string | null) => {
      const sequence = ++evidenceSequence.current
      // Основания разных интервью не смешиваются: чистим до запроса.
      setEvidence([])
      setEvidenceError(null)
      setEvidenceNotAvailable(false)
      if (!interviewId) {
        setEvidenceLoading(false)
        return
      }
      setEvidenceLoading(true)
      try {
        const data = await listInterviewEvidence(runId, interviewId)
        if (sequence !== evidenceSequence.current) return
        setEvidence(Array.isArray(data) ? data : [])
      } catch (cause) {
        if (sequence !== evidenceSequence.current) return
        setEvidence([])
        // 501 - раздел ещё не включён; прочие ошибки видны отдельной строкой,
        // но блок остаётся нейтральным и не роняет экран.
        if (cause instanceof ApiRequestError && cause.status === 501) {
          setEvidenceNotAvailable(true)
        } else {
          setEvidenceError(describeInterviewsError(cause))
        }
      } finally {
        if (sequence === evidenceSequence.current) setEvidenceLoading(false)
      }
    },
    [runId],
  )

  useEffect(() => {
    // Загрузка асинхронная; «поздний» ответ прошлого интервью отбрасывается.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void loadEvidence(selectedId)
    return () => {
      evidenceSequence.current += 1
    }
  }, [loadEvidence, selectedId, evidenceToken])

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

        {canStart && (showStart ? (
          <form className="space-y-2 rounded-lg border p-3" onSubmit={(event) => {
            event.preventDefault()
            setStarting(true)
            setStartError(null)
            void startRunInterview(runId, name.trim(), role.trim()).then(() => {
              setShowStart(false)
              setName("")
              setRole("")
              onStarted?.()
            }).catch((cause: unknown) => setStartError(describeInterviewsError(cause))).finally(() => setStarting(false))
          }}>
            <p className="text-xs text-muted-foreground">Начало нового интервью завершит текущий диалог. История сохранится.</p>
            <label className="block text-sm">Имя респондента<input className="mt-1 w-full rounded border p-2" value={name} maxLength={120} onChange={(event) => setName(event.target.value)} /></label>
            <label className="block text-sm">Роль респондента<input className="mt-1 w-full rounded border p-2" value={role} maxLength={120} onChange={(event) => setRole(event.target.value)} /></label>
            {startError && <p role="alert" className="text-sm text-destructive">{startError}</p>}
            <Button type="submit" size="sm" disabled={starting}>Начать интервью</Button>{" "}
            <Button type="button" size="sm" variant="outline" disabled={starting} onClick={() => setShowStart(false)}>Отмена</Button>
          </form>
        ) : <Button size="sm" onClick={() => setShowStart(true)}>Новый респондент</Button>)}

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
              <ul className="max-h-[520px] space-y-3 overflow-y-auto pr-1">
                {messages.map((message) => {
                  const fromUser = message.role === "user"
                  const citations = (message.citations ?? []).filter(
                    (citation) => citation && typeof citation.quote === "string" && citation.quote.trim(),
                  )
                  return (
                    <li key={message.id} className={`flex ${fromUser ? "justify-end" : "justify-start"}`}>
                      <div
                        className={`max-w-[80%] space-y-1 rounded-lg px-3 py-2 text-sm ${
                          fromUser
                            ? "bg-primary text-primary-foreground"
                            : "bg-muted text-foreground"
                        }`}
                      >
                        <div
                          className={`flex flex-wrap items-center gap-2 text-[11px] ${
                            fromUser ? "text-primary-foreground/80" : "text-muted-foreground"
                          }`}
                        >
                          <span className="font-medium">{fromUser ? "Респондент" : "Интервьюер"}</span>
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
                                className={`rounded-md px-2 py-1 text-xs ${
                                  fromUser
                                    ? "bg-primary-foreground/10 text-primary-foreground/90"
                                    : "border bg-background/70 text-muted-foreground"
                                }`}
                              >
                                {citation.title ? `${citation.title}: ` : ""}«{citation.quote}»
                              </li>
                            ))}
                          </ul>
                        )}
                      </div>
                    </li>
                  )
                })}
              </ul>
            )}

            <section
              className="space-y-2 rounded-lg border bg-muted/10 p-3"
              aria-label="Ответы, ставшие основаниями"
            >
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h3 className="text-sm font-medium">Ответы, ставшие основаниями</h3>
                <div className="flex items-center gap-2">
                  <span className="text-xs text-muted-foreground">
                    {evidenceLoading ? "Загрузка…" : `Обоснований: ${evidence.length}`}
                  </span>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label="Обновить основания интервью"
                    disabled={evidenceLoading}
                    onClick={() => setEvidenceToken((token) => token + 1)}
                  >
                    <RefreshCwIcon className="size-4" />
                  </Button>
                </div>
              </div>
              {evidenceLoading ? (
                <div className="space-y-1.5">
                  <Skeleton className="h-12 w-full" />
                  <Skeleton className="h-12 w-3/4" />
                </div>
              ) : evidenceError ? (
                <div className="space-y-2">
                  <p className="text-sm text-muted-foreground">
                    Основания ещё не связаны с ответами.
                  </p>
                  <p className="text-xs text-destructive" role="alert">
                    {evidenceError}
                  </p>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => setEvidenceToken((token) => token + 1)}
                  >
                    Повторить загрузку оснований
                  </Button>
                </div>
              ) : evidence.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  Основания ещё не связаны с ответами.
                  {evidenceNotAvailable && " Раздел пока не поддерживается сервером."}
                </p>
              ) : (
                <ul className="space-y-1.5">
                  {evidence.map((item) => {
                    const status = item.status ?? "pending"
                    return (
                      <li
                        key={item.evidence_id}
                        className="space-y-1 rounded-lg border bg-background/60 p-2.5 text-sm"
                      >
                        <blockquote className="whitespace-pre-wrap break-words text-muted-foreground">
                          «{item.quote}»
                        </blockquote>
                        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
                          <span
                            className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${EVIDENCE_STATUS_CLASS[status] ?? "bg-muted text-muted-foreground"}`}
                          >
                            {EVIDENCE_STATUS_LABEL[status] ?? status}
                          </span>
                          {onFocusNode ? (
                            <button
                              type="button"
                              onClick={() => onFocusNode(item.node_id)}
                              aria-label={`Перейти к узлу ${item.node_title}`}
                              className="min-w-0 break-words text-left font-medium underline underline-offset-4 hover:text-primary"
                            >
                              {item.node_title}
                            </button>
                          ) : (
                            <span className="min-w-0 break-words font-medium">{item.node_title}</span>
                          )}
                          <span className="text-muted-foreground">
                            {nodeTypeLabel(item.node_type)}
                          </span>
                          <span className="min-w-0 break-words text-muted-foreground">
                            Источник: {item.source_title || "без названия"}
                          </span>
                        </div>
                      </li>
                    )
                  })}
                </ul>
              )}
            </section>
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
