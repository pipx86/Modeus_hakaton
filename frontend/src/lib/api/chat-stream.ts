import { apiBaseUrl, type ChatMessage, type RunEvent, type RunStatus } from "./client"

export type StreamState = "open" | "reconnecting" | "failed"

export interface ChatStreamHandlers {
  onThinking: () => void
  onAgentMessage: (message: ChatMessage) => void
  onRunStatus: (status: RunStatus) => void
  onStateChange: (state: StreamState) => void
}

const MAX_RECONNECT_FAILURES = 3

export function connectRunEvents(runId: string, handlers: ChatStreamHandlers): () => void {
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