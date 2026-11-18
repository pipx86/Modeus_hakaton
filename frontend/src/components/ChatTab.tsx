import { useCallback, useEffect, useRef, useState, type KeyboardEvent } from "react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import { apiClient, type ChatMessage, type RunStatus } from "@/lib/api/client"
import { connectRunEvents, type StreamState } from "@/lib/api/chat-stream"
import { Loader2Icon, RefreshCwIcon, SendIcon, WifiOffIcon } from "lucide-react"

interface ChatTabProps {
  runId: string
  active: boolean
  runStatus?: RunStatus
}

export function ChatTab({ runId, active, runStatus }: ChatTabProps) {
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [draft, setDraft] = useState("")
  const [sending, setSending] = useState(false)
  const [thinking, setThinking] = useState(false)
  const [streamState, setStreamState] = useState<StreamState>("open")
  const bottomRef = useRef<HTMLDivElement>(null)
  const knownIds = useRef<Set<string>>(new Set())
  const previousStreamState = useRef<StreamState>("open")

  const appendMessages = useCallback((incoming: ChatMessage[]) => {
    // Бэкенд может вернуть объект ошибки вместо массива — не роняем вкладку.
    if (!Array.isArray(incoming)) return
    const fresh = incoming.filter((message) => !knownIds.current.has(message.id))
    if (fresh.length === 0) return
    for (const message of fresh) knownIds.current.add(message.id)
    setMessages((prev) => [...prev, ...fresh])
  }, [])

  const loadHistory = useCallback(async () => {
    try {
      const { data, error } = await apiClient.GET("/runs/{id}/messages", {
        params: { path: { id: runId } },
      })
      if (Array.isArray(data)) {
        appendMessages(data)
        setLoadError(null)
      } else {
        setLoadError(
          error?.code === "NOT_FOUND"
            ? "Чат ещё не открыт: агент не задавал вопросов по этому исследованию."
            : (error?.message ?? "История чата недоступна."),
        )
      }
    } catch {
      setLoadError("История чата недоступна: сервер не отвечает.")
    } finally {
      setLoading(false)
    }
  }, [runId, appendMessages])

  useEffect(() => {
    // Новый прогон — новый чат: старые id не должны блокировать загрузку истории.
    knownIds.current = new Set()
    setMessages([])
  }, [runId])

  useEffect(() => {
    // История загружается из API; состояние обновляется асинхронно после ответа.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void loadHistory()
  }, [loadHistory])

  useEffect(() => {
    if (!active) return
    let pollTimer: number | undefined

    const disconnect = connectRunEvents(runId, {
      onThinking: () => setThinking(true),
      onAgentMessage: (message) => {
        appendMessages([message])
        setThinking(false)
      },
      onRunStatus: (status: RunStatus) => {
        if (status === "done" || status === "failed") setThinking(false)
      },
      onStateChange: (state) => {
        const previous = previousStreamState.current
        previousStreamState.current = state
        setStreamState(state)
        if (state === "open") {
          if (previous !== "open") toast.success("Соединение восстановлено")
          void loadHistory()
        }
        if (state === "reconnecting" && previous === "open") {
          toast.warning("Соединение потеряно", {
            description: "Пробую переподключиться к серверу.",
          })
        }
        if (state === "failed") {
          if (previous !== "failed") {
            toast.error("Соединение с сервером потеряно", {
              description: "Новые сообщения приходят опросом раз в 5 секунд.",
            })
          }
          pollTimer = window.setInterval(async () => {
            const { data } = await apiClient.GET("/runs/{id}/messages", {
              params: { path: { id: runId } },
            })
            if (data) appendMessages(data)
          }, 5000)
        }
      },
    })

    return () => {
      disconnect()
      if (pollTimer !== undefined) window.clearInterval(pollTimer)
    }
  }, [runId, active, appendMessages, loadHistory])

  useEffect(() => {
    bottomRef.current?.scrollIntoView?.({ behavior: "smooth", block: "end" })
  }, [messages, thinking])

  const send = async () => {
    const content = draft.trim()
    if (!content || sending) return
    setSending(true)
    try {
      const { data, error } = await apiClient.POST("/runs/{id}/messages", {
        params: { path: { id: runId } },
        body: { content },
      })
      if (error) {
        toast.error("Не удалось отправить ответ", { description: error.message })
        return
      }
      if (data) appendMessages([data])
      setDraft("")
      setThinking(true)
    } catch {
      toast.error("Сервер недоступен", { description: "Ответ не отправлен." })
    } finally {
      setSending(false)
    }
  }

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault()
      void send()
    }
  }

  if (loading) {
    return (
      <div className="space-y-2">
        <Skeleton className="h-16 w-2/3" />
        <Skeleton className="ml-auto h-16 w-1/2" />
        <Skeleton className="h-16 w-2/3" />
      </div>
    )
  }

  if (loadError && messages.length === 0) {
    return (
      <div className="space-y-3 rounded-xl border border-destructive/50 p-8 text-center">
        <p className="text-sm text-destructive">{loadError}</p>
        <Button
          variant="outline"
          onClick={() => {
            setLoading(true)
            void loadHistory()
          }}
        >
          <RefreshCwIcon className="size-4" />
          Повторить
        </Button>
      </div>
    )
  }

  return (
    <div className="space-y-3">
      {(streamState !== "open" || runStatus === "failed" || runStatus === "degraded") && (
        <div className="flex items-center gap-2 rounded-lg border border-destructive/50 px-3 py-2 text-xs text-destructive">
          <WifiOffIcon className="size-3.5 shrink-0" />
          {runStatus === "failed" || runStatus === "degraded"
            ? "Прогон завершился ошибкой - чат закрыт."
            : streamState === "reconnecting"
              ? "Соединение потеряно - переподключаюсь…"
              : "SSE-поток недоступен: новые сообщения приходят опросом раз в 5 секунд."}
          {streamState === "failed" && (
            <Button
              variant="outline"
              size="sm"
              className="ml-auto h-6 px-2 text-xs"
              onClick={() => void loadHistory()}
            >
              <RefreshCwIcon className="size-3" />
              Обновить
            </Button>
          )}
        </div>
      )}

      <div className="max-h-[480px] space-y-3 overflow-y-auto rounded-xl border p-4">
        {messages.length === 0 && !thinking && (
          <p className="py-8 text-center text-sm text-muted-foreground">
            Агент ещё не задал первый вопрос - он появится здесь.
          </p>
        )}
        {messages.map((message) => (
          <div
            key={message.id}
            className={message.role === "user" ? "flex justify-end" : "flex justify-start"}
          >
            <div
              className={
                message.role === "user"
                  ? "max-w-[80%] rounded-lg bg-primary px-3 py-2 text-primary-foreground"
                  : "max-w-[80%] rounded-lg bg-muted px-3 py-2 text-foreground"
              }
            >
              <p className="whitespace-pre-wrap text-sm">{message.content}</p>
              {message.citations && message.citations.length > 0 && (
                <div className="mt-2 space-y-1 border-t border-border/60 pt-2">
                  <p className="text-xs font-medium text-muted-foreground">
                    На чём основан ответ
                  </p>
                  <ul className="space-y-1">
                    {message.citations.map((citation, index) => (
                      <li key={`${citation.quote}-${index}`} className="text-xs">
                        <span className="text-muted-foreground">{citation.title}: </span>
                        <span className="italic">«{citation.quote}»</span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </div>
          </div>
        ))}
        {thinking && (
          <div className="flex justify-start">
            <div className="flex items-center gap-1.5 rounded-lg bg-muted px-3 py-2">
              <span className="size-1.5 animate-bounce rounded-full bg-muted-foreground" />
              <span className="size-1.5 animate-bounce rounded-full bg-muted-foreground [animation-delay:120ms]" />
              <span className="size-1.5 animate-bounce rounded-full bg-muted-foreground [animation-delay:240ms]" />
              <span className="ml-1 text-xs text-muted-foreground">агент думает…</span>
            </div>
          </div>
        )}
        <div ref={bottomRef} />
      </div>

      <div className="flex items-end gap-2">
        <Textarea
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={onKeyDown}
          rows={2}
          placeholder="Ответ на вопрос агента… Enter - отправить, Shift+Enter - новая строка"
        />
        <Button onClick={() => void send()} disabled={!draft.trim() || sending} className="shrink-0">
          {sending ? <Loader2Icon className="size-4 animate-spin" /> : <SendIcon className="size-4" />}
          Отправить
        </Button>
      </div>
    </div>
  )
}
