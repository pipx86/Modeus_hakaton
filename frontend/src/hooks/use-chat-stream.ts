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

  const interviewFinished =
    stage === "analysis" || stage === "synthesis" || stage === "review" || stage === "done"
  const chatClosed = runStatus === "done" || stage === "done"

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
    // Сброс при смене runId — синхронизация с внешним run, а не каскадный рендер.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setMessages([])
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
  }, [runId, active, appendMessages, loadHistory, interviewFinished])

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
