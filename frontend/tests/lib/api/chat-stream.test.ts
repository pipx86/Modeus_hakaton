/**
 * Тесты клиента SSE без сети и без компонентов: проверяются URL потока,
 * трансляция событий в колбэки, лимит реконнектов и закрытие при размонтировании.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { connectRunEvents } from "@/lib/api/chat-stream"

type Listener = (event: { data: string }) => void

class FakeEventSource {
  static instances: FakeEventSource[] = []

  listeners = new Map<string, Listener[]>()
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  closed = false
  url: string

  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }

  addEventListener(type: string, listener: Listener) {
    const list = this.listeners.get(type) ?? []
    list.push(listener)
    this.listeners.set(type, list)
  }

  emit(type: string, data: unknown = {}) {
    for (const listener of this.listeners.get(type) ?? []) {
      listener({ data: JSON.stringify(data) })
    }
  }

  close() {
    this.closed = true
  }
}

function makeHandlers() {
  return {
    onThinking: vi.fn(),
    onAgentMessage: vi.fn(),
    onRunStatus: vi.fn(),
    onStateChange: vi.fn(),
  }
}

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal("EventSource", FakeEventSource)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("connectRunEvents", () => {
  it("подписывается на поток события запуска", () => {
    connectRunEvents("run-7", makeHandlers())

    expect(FakeEventSource.instances).toHaveLength(1)
    expect(FakeEventSource.instances[0].url).toMatch(/\/api\/v1\/runs\/run-7\/events$/)
    expect(FakeEventSource.instances[0].url).toMatch(/^(\/|https?:\/\/)/)
  })

  it("открытие потока переводит состояние в open", () => {
    const handlers = makeHandlers()
    connectRunEvents("run-7", handlers)

    FakeEventSource.instances[0].onopen?.()

    expect(handlers.onStateChange).toHaveBeenCalledWith("open")
  })

  it("пробрасывает agent_thinking и agent_message", () => {
    const handlers = makeHandlers()
    connectRunEvents("run-7", handlers)
    const source = FakeEventSource.instances[0]

    source.emit("agent_thinking")
    source.emit("agent_message", {
      type: "agent_message",
      message: { id: "m1", role: "agent", content: "Вопрос", created_at: "2026-11-10T10:00:00Z" },
    })

    expect(handlers.onThinking).toHaveBeenCalledTimes(1)
    expect(handlers.onAgentMessage).toHaveBeenCalledWith(
      expect.objectContaining({ id: "m1", content: "Вопрос" }),
    )
  })

  it("пробрасывает run_status и игнорирует ping", () => {
    const handlers = makeHandlers()
    connectRunEvents("run-7", handlers)
    const source = FakeEventSource.instances[0]

    source.emit("ping")
    source.emit("run_status", { type: "run_status", status: "done" })

    expect(handlers.onRunStatus).toHaveBeenCalledWith("done")
    expect(handlers.onThinking).not.toHaveBeenCalled()
  })

  it("после трёх ошибок закрывает поток и сообщает failed", () => {
    const handlers = makeHandlers()
    connectRunEvents("run-7", handlers)
    const source = FakeEventSource.instances[0]

    source.onerror?.()
    expect(handlers.onStateChange).toHaveBeenLastCalledWith("reconnecting")

    source.onerror?.()
    expect(handlers.onStateChange).toHaveBeenLastCalledWith("reconnecting")

    source.onerror?.()
    expect(handlers.onStateChange).toHaveBeenLastCalledWith("failed")
    expect(source.closed).toBe(true)
  })

  it("успешное переподключение сбрасывает счётчик ошибок", () => {
    const handlers = makeHandlers()
    connectRunEvents("run-7", handlers)
    const source = FakeEventSource.instances[0]

    source.onerror?.()
    source.onopen?.()
    source.onerror?.()
    source.onopen?.()
    source.onerror?.()

    expect(handlers.onStateChange).toHaveBeenLastCalledWith("reconnecting")
    expect(source.closed).toBe(false)
  })

  it("возвращённая функция закрывает поток", () => {
    const disconnect = connectRunEvents("run-7", makeHandlers())

    disconnect()

    expect(FakeEventSource.instances[0].closed).toBe(true)
  })
})
