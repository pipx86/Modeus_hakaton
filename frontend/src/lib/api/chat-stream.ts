/**
 * @file chat-stream.ts
 * @description Разбор SSE-потока чата: события сообщений, ошибок и завершения.
 *
 * В браузере используем fetch + ReadableStream: EventSource не умеет передавать
 * заголовок Authorization, поэтому напрямую к API он работает только через
 * dev-прокси. В тестах сохранён EventSource-путь (его подменяет фейк).
 */

import { getValidToken } from "@/lib/auth"
import { apiBaseUrl, apiToken, type ChatMessage, type RunEvent, type RunStatus } from "./client"

export type StreamState = "open" | "reconnecting" | "failed"

export interface ChatStreamHandlers {
  onThinking: () => void
  onAgentMessage: (message: ChatMessage) => void
  onRunStatus: (status: RunStatus) => void
  onStateChange: (state: StreamState) => void
}

const MAX_RECONNECT_FAILURES = 3

function dispatchEvent(raw: string, handlers: ChatStreamHandlers): void {
  let event = "message"
  const data: string[] = []
  for (const line of raw.replace(/\r\n/g, "\n").split("\n")) {
    if (!line || line.startsWith(":")) continue
    const colon = line.indexOf(":")
    const field = colon === -1 ? line : line.slice(0, colon)
    const value = colon === -1 ? "" : line.slice(colon + 1).replace(/^ /, "")
    if (field === "event") event = value
    if (field === "data") data.push(value)
  }
  if (data.length === 0) return
  const payloadText = data.join("\n")
  if (event === "agent_thinking") {
    handlers.onThinking()
    return
  }
  try {
    const payload = JSON.parse(payloadText) as RunEvent
    if (event === "agent_message" && payload.message) handlers.onAgentMessage(payload.message)
    if (event === "run_status" && payload.status) handlers.onRunStatus(payload.status)
  } catch {
    // Битое событие не должно ронять весь поток.
  }
}

function connectWithEventSource(runId: string, handlers: ChatStreamHandlers): () => void {
  let failures = 0
  let closedByUs = false

  const source = new EventSource(`${apiBaseUrl}/runs/${runId}/events`)

  source.onopen = () => {
    failures = 0
    handlers.onStateChange("open")
  }

  source.addEventListener("agent_thinking", () => handlers.onThinking())

  source.addEventListener("agent_message", (event) => {
    const payload = JSON.parse((event as MessageEvent).data) as RunEvent
    if (payload.message) handlers.onAgentMessage(payload.message)
  })

  source.addEventListener("run_status", (event) => {
    const payload = JSON.parse((event as MessageEvent).data) as RunEvent
    if (payload.status) handlers.onRunStatus(payload.status)
  })

  source.addEventListener("ping", () => undefined)

  source.onerror = () => {
    if (closedByUs) return
    failures += 1
    if (failures >= MAX_RECONNECT_FAILURES) {
      source.close()
      handlers.onStateChange("failed")
      return
    }
    handlers.onStateChange("reconnecting")
  }

  return () => {
    closedByUs = true
    source.close()
  }
}

function connectWithFetch(runId: string, handlers: ChatStreamHandlers): () => void {
  let closedByUs = false
  let failures = 0
  let controller: AbortController | null = null
  let reconnectTimer: number | null = null

  const connect = async () => {
    if (closedByUs) return
    controller = new AbortController()
    try {
      const token = getValidToken() ?? apiToken
      const response = await fetch(`${apiBaseUrl}/runs/${runId}/events`, {
        headers: {
          Accept: "text/event-stream",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        signal: controller.signal,
      })
      if (!response.ok || !response.body) {
        throw new Error(`SSE HTTP ${response.status}`)
      }
      failures = 0
      handlers.onStateChange("open")
      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ""
      while (!closedByUs) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        let boundary = buffer.indexOf("\n\n")
        while (boundary !== -1) {
          const frame = buffer.slice(0, boundary)
          buffer = buffer.slice(boundary + 2)
          dispatchEvent(frame, handlers)
          boundary = buffer.indexOf("\n\n")
        }
      }
      if (!closedByUs) scheduleReconnect()
    } catch {
      if (closedByUs) return
      failures += 1
      if (failures >= MAX_RECONNECT_FAILURES) {
        handlers.onStateChange("failed")
        return
      }
      handlers.onStateChange("reconnecting")
      scheduleReconnect()
    }
  }

  const scheduleReconnect = () => {
    if (closedByUs || reconnectTimer !== null) return
    reconnectTimer = window.setTimeout(() => {
      reconnectTimer = null
      void connect()
    }, 1000)
  }

  void connect()

  return () => {
    closedByUs = true
    controller?.abort()
    if (reconnectTimer !== null) window.clearTimeout(reconnectTimer)
  }
}

export function connectRunEvents(runId: string, handlers: ChatStreamHandlers): () => void {
  if (import.meta.env.MODE === "test") {
    return connectWithEventSource(runId, handlers)
  }
  return connectWithFetch(runId, handlers)
}
