/**
 * @file use-chat-stream.ts
 * @description Состояние чата интервью: история из API, SSE-поток, отправка ответов.
 */

import { useCallback, useEffect, useRef, useState } from "react"
import { toast } from "sonner"
import { apiClient, type ChatMessage, type Run, type RunStatus } from "@/lib/api/client"
import { connectRunEvents, type StreamState } from "@/lib/api/chat-stream"

interface UseChatStreamOptions {
  runId: string
  active: boolean
  runStatus?: RunStatus
  stage?: Run["stage"]
}

/**
 * Держит историю чата, подписку на SSE и отправку сообщений.
 *
 * История хранится на бэкенде, поэтому после завершения интервью её можно
 * перезагрузить и просмотреть; поле ввода блокируется, когда прогон закрыт.
 */
export function useChatStream({ runId, active, runStatus, stage }: UseChatStreamOptions) {
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [draft, setDraft] = useState("")
  const [sending, setSending] = useState(false)
  const [thinking, setThinking] = useState(false)
  const [streamState, setStreamState] = useState<StreamState>("open")
  const knownIds = useRef<Set<string>>(new Set())
  const previousStreamState = useRef<StreamState>("open")
  // Текущий runId и номер запроса истории: ответы старого прогона игнорируются.
  const runIdRef = useRef(runId)
  const historySeq = useRef(0)
  // false после размонтирования - запрещает setState из асинхронного send().
  const mounted = useRef(true)

  const interviewFinished =
    stage === "analysis" || stage === "synthesis" || stage === "review" || stage === "done"
  const chatClosed = runStatus === "done" || stage === "done"

  const appendMessages = useCallback((incoming: ChatMessage[]) => {
    // Бэкенд может вернуть объект ошибки вместо массива - не роняем вкладку.
    if (!Array.isArray(incoming)) return
    // Ответ агента пришёл (в том числе опросом при SSE-fallback) - индикатор
    // "думает" больше не актуален.
    if (incoming.some((message) => message.role === "agent")) setThinking(false)
    const fresh = incoming.filter((message) => !knownIds.current.has(message.id))
    if (fresh.length === 0) return
    for (const message of fresh) knownIds.current.add(message.id)
    setMessages((prev) => [...prev, ...fresh])
  }, [])

  const loadHistory = useCallback(async () => {
    const requestedRunId = runId
    const requestId = ++historySeq.current
    const isCurrent = () =>
      requestId === historySeq.current && requestedRunId === runIdRef.current
    try {
      const { data, error } = await apiClient.GET("/runs/{id}/messages", {
        params: { path: { id: runId } },
      })
      if (!isCurrent()) return
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
      if (!isCurrent()) return
      setLoadError("История чата недоступна: сервер не отвечает.")
    } finally {
      if (isCurrent()) setLoading(false)
    }
  }, [runId, appendMessages])

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])

  useEffect(() => {
    // Новый прогон - новый чат: старые id и ответы не должны попасть в него.
    runIdRef.current = runId
    knownIds.current = new Set()
    historySeq.current += 1
    previousStreamState.current = "open"
    // Сброс при смене runId - синхронизация с внешним run, а не каскадный рендер.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setMessages([])
    setLoading(true)
    setLoadError(null)
    setThinking(false)
  }, [runId])

  useEffect(() => {
    // История загружается из API; состояние обновляется асинхронно после ответа.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void loadHistory()
  }, [loadHistory])

  // После завершения прогона подтягиваем финальные сообщения: пользователь может
  // вернуться на вкладку позже и увидеть весь диалог.
  useEffect(() => {
    if (!chatClosed) return
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void loadHistory()
  }, [chatClosed, loadHistory])

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
          if (previous !== "open" && !interviewFinished) toast.success("Соединение восстановлено")
          void loadHistory()
        }
        if (state === "reconnecting" && previous === "open" && !interviewFinished) {
          toast.warning("Соединение потеряно", {
            description: "Пробую переподключиться к серверу.",
          })
        }
        if (state === "failed") {
          if (previous !== "failed" && !interviewFinished) {
            toast.error("Соединение с сервером потеряно", {
              description: "Новые сообщения приходят опросом раз в 5 секунд.",
            })
          }
          if (pollTimer !== undefined) return
          pollTimer = window.setInterval(async () => {
            const { data } = await apiClient.GET("/runs/{id}/messages", {
              params: { path: { id: runId } },
            })
            // Ответ старого прогона не должен попасть в новый чат.
            if (!mounted.current || runIdRef.current !== runId) return
            // appendMessages снимает thinking, когда приходит сообщение агента.
            if (data) appendMessages(data)
          }, 5000)
        }
      },
    })

    return () => {
      disconnect()
      if (pollTimer !== undefined) window.clearInterval(pollTimer)
    }
  }, [runId, active, appendMessages, loadHistory, interviewFinished])

  const send = async () => {
    const content = draft
    if (!content.trim() || sending) return
    const requestedRunId = runId
    setSending(true)
    try {
      const { data, error } = await apiClient.POST("/runs/{id}/messages", {
        params: { path: { id: runId } },
        body: { content },
      })
      // После размонтирования или смены прогона ответ уже не актуален.
      if (!mounted.current || runIdRef.current !== requestedRunId) return
      if (error) {
        toast.error("Не удалось отправить ответ", { description: error.message })
        return
      }
      if (data) appendMessages([data])
      setDraft("")
      setThinking(true)
    } catch {
      if (!mounted.current || runIdRef.current !== requestedRunId) return
      toast.error("Сервер недоступен", { description: "Ответ не отправлен." })
    } finally {
      if (mounted.current) setSending(false)
    }
  }

  return {
    messages,
    loading,
    loadError,
    draft,
    setDraft,
    sending,
    thinking,
    streamState,
    chatClosed,
    interviewFinished,
    send,
    retry: loadHistory,
  }
}
