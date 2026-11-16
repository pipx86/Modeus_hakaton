/**
 * Тесты вкладки чата: история, отправка, индикатор «агент думает», обрыв SSE
 * и переход на поллинг. Сеть подменяется fetch-стабом, EventSource — фейком.
 */
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { ChatTab } from "@/components/ChatTab"

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

vi.stubGlobal("EventSource", FakeEventSource)

afterEach(() => {
  FakeEventSource.instances = []
})

function stubChat() {
  return setFetchHandler(async (url, init) => {
    const method = init.method ?? "GET"
    if (url.endsWith("/runs/7/messages") && method === "GET") {
      return {
        status: 200,
        body: [
          {
            id: "m1",
            role: "agent",
            content: "Какие задачи вы решаете чаще всего?",
            created_at: "2026-11-10T10:00:00Z",
          },
        ],
      }
    }
    if (url.endsWith("/runs/7/messages") && method === "POST") {
      const body = JSON.parse(String(init.body)) as { content: string }
      return {
        status: 201,
        body: { id: "m2", role: "user", content: body.content, created_at: "2026-11-10T10:01:00Z" },
      }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

function renderTab() {
  return render(<ChatTab runId="7" active />)
}

describe("ChatTab", () => {
  it("показывает историю сообщений запуска", async () => {
    stubChat()
    renderTab()

    expect(await screen.findByText("Какие задачи вы решаете чаще всего?")).toBeInTheDocument()
  })

  it("отправляет ответ и добавляет его в ленту", async () => {
    stubChat()
    renderTab()
    await screen.findByText("Какие задачи вы решаете чаще всего?")

    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Анализирую данные" } })
    fireEvent.click(screen.getByRole("button", { name: "Отправить" }))

    expect(await screen.findByText("Анализирую данные")).toBeInTheDocument()
    const post = fetchCalls().find(
      (call) => call.url.endsWith("/runs/7/messages") && call.init.method === "POST",
    )
    expect(post).toBeDefined()
    expect(JSON.parse(String(post!.init.body))).toEqual({ content: "Анализирую данные" })
  })

  it("не отправляет пустой ответ", async () => {
    stubChat()
    renderTab()
    await screen.findByText("Какие задачи вы решаете чаще всего?")

    fireEvent.click(screen.getByRole("button", { name: "Отправить" }))

    const posts = fetchCalls().filter((call) => call.init.method === "POST")
    expect(posts).toHaveLength(0)
  })

  it("отправляет ответ по Enter и переносит строку по Shift+Enter", async () => {
    stubChat()
    renderTab()
    await screen.findByText("Какие задачи вы решаете чаще всего?")
    const box = screen.getByRole("textbox")

    fireEvent.change(box, { target: { value: "Черновик" } })
    fireEvent.keyDown(box, { key: "Enter", shiftKey: true })
    expect(fetchCalls().filter((call) => call.init.method === "POST")).toHaveLength(0)

    fireEvent.keyDown(box, { key: "Enter" })
    expect(await screen.findByText("Черновик")).toBeInTheDocument()
  })

  it("индикатор «агент думает» горит после отправки и гаснет по agent_message", async () => {
    stubChat()
    renderTab()
    await screen.findByText("Какие задачи вы решаете чаще всего?")

    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Анализирую данные" } })
    fireEvent.click(screen.getByRole("button", { name: "Отправить" }))
    expect(await screen.findByText("агент думает…")).toBeInTheDocument()

    FakeEventSource.instances[0].emit("agent_message", {
      type: "agent_message",
      message: {
        id: "m3",
        role: "agent",
        content: "Уточню: какие инструменты используете?",
        created_at: "2026-11-10T10:02:00Z",
      },
    })

    expect(await screen.findByText("Уточню: какие инструменты используете?")).toBeInTheDocument()
    expect(screen.queryByText("агент думает…")).not.toBeInTheDocument()
  })

  it("обрыв соединения показывает баннер и ресинкает историю при возврате", async () => {
    stubChat()
    renderTab()
    await screen.findByText("Какие задачи вы решаете чаще всего?")
    const source = FakeEventSource.instances[0]

    source.onerror?.()
    expect(await screen.findByText(/Соединение потеряно/)).toBeInTheDocument()

    const getsBefore = fetchCalls().filter((call) => call.url.endsWith("/runs/7/messages")).length
    source.onopen?.()
    await waitFor(() => {
      const getsAfter = fetchCalls().filter((call) => call.url.endsWith("/runs/7/messages")).length
      expect(getsAfter).toBeGreaterThan(getsBefore)
    })
    expect(screen.queryByText(/Соединение потеряно/)).not.toBeInTheDocument()
  })

  it("после лимита неудачных переподключений включает опрос и закрывает поток", async () => {
    stubChat()
    renderTab()
    await screen.findByText("Какие задачи вы решаете чаще всего?")
    const source = FakeEventSource.instances[0]

    source.onerror?.()
    source.onerror?.()
    source.onerror?.()

    expect(await screen.findByText(/SSE-поток недоступен/)).toBeInTheDocument()
    expect(source.closed).toBe(true)
  })

  it("ошибка загрузки истории показывает баннер с кнопкой повтора", async () => {
    setFetchHandler(async () => {
      throw new Error("offline")
    })
    renderTab()

    expect(await screen.findByText(/История чата недоступна/)).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Повторить/ })).toBeInTheDocument()
  })
})
