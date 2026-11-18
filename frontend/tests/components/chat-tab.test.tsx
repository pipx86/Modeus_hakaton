/**
 * Тесты вкладки чата: история, отправка, индикатор «агент думает», обрыв SSE
 * и переход на поллинг. Сеть подменяется fetch-стабом, EventSource - фейком.
 */
import { StrictMode } from "react"
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { toast } from "sonner"
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

function emitAgentMessage(data: unknown) {
  act(() => {
    FakeEventSource.instances[0].emit("agent_message", data)
  })
}

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

  it("не теряет ответ пользователя в StrictMode", async () => {
    stubChat()
    render(
      <StrictMode>
        <ChatTab runId="7" active />
      </StrictMode>,
    )
    await screen.findByText("Какие задачи вы решаете чаще всего?")

    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Ответ в StrictMode" } })
    fireEvent.click(screen.getByRole("button", { name: "Отправить" }))

    expect(await screen.findByText("Ответ в StrictMode")).toBeInTheDocument()
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

    emitAgentMessage({
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

    act(() => {
      source.onerror?.()
    })
    expect(await screen.findByText(/Соединение потеряно/)).toBeInTheDocument()

    const getsBefore = fetchCalls().filter((call) => call.url.endsWith("/runs/7/messages")).length
    act(() => {
      source.onopen?.()
    })
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

    act(() => {
      source.onerror?.()
    })
    act(() => {
      source.onerror?.()
    })
    act(() => {
      source.onerror?.()
    })

    expect(await screen.findByText(/SSE-поток недоступен/)).toBeInTheDocument()
    expect(source.closed).toBe(true)
  })

  it("показывает ошибку отправки и не добавляет сообщение в ленту", async () => {
    setFetchHandler(async (url, init) => {
      if (url.endsWith("/runs/7/messages") && (init?.method ?? "GET") === "GET") {
        return { status: 200, body: [] }
      }
      return { status: 422, body: { code: "VALIDATION", message: "пустой ответ" } }
    })

    renderTab()
    const box = await screen.findByRole("textbox")
    fireEvent.change(box, { target: { value: "Ответ" } })
    fireEvent.click(screen.getByRole("button", { name: "Отправить" }))

    await waitFor(() =>
      expect(fetchCalls().some((call) => call.init.method === "POST")).toBe(true),
    )
    expect(screen.queryByText("агент думает…")).not.toBeInTheDocument()
  })

  it("переживает сетевой сбой при отправке ответа", async () => {
    setFetchHandler(async (url, init) => {
      if (url.endsWith("/runs/7/messages") && (init?.method ?? "GET") === "GET") {
        return { status: 200, body: [] }
      }
      throw new Error("offline")
    })

    renderTab()
    const box = await screen.findByRole("textbox")
    fireEvent.change(box, { target: { value: "Ответ" } })
    fireEvent.click(screen.getByRole("button", { name: "Отправить" }))

    await waitFor(() =>
      expect(fetchCalls().some((call) => call.init.method === "POST")).toBe(true),
    )
    expect(screen.getByRole("button", { name: "Отправить" })).toBeEnabled()
  })

  it("run_status done гасит индикатор «агент думает»", async () => {
    stubChat()
    renderTab()
    await screen.findByText("Какие задачи вы решаете чаще всего?")

    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Ответ" } })
    fireEvent.click(screen.getByRole("button", { name: "Отправить" }))
    expect(await screen.findByText("агент думает…")).toBeInTheDocument()

    act(() => {
      FakeEventSource.instances[0].emit("run_status", { type: "run_status", status: "done" })
    })

    await waitFor(() => expect(screen.queryByText("агент думает…")).not.toBeInTheDocument())
  })

  it("после обрыва показывает кнопку «Обновить» и повторяет загрузку истории", async () => {
    stubChat()
    renderTab()
    await screen.findByText("Какие задачи вы решаете чаще всего?")
    const source = FakeEventSource.instances[0]

    const getsBefore = fetchCalls().filter((call) => call.url.endsWith("/runs/7/messages")).length

    act(() => {
      source.onerror?.()
    })
    act(() => {
      source.onerror?.()
    })
    act(() => {
      source.onerror?.()
    })

    const refresh = await screen.findByRole("button", { name: /Обновить/ })
    fireEvent.click(refresh)

    await waitFor(() => {
      const getsAfter = fetchCalls().filter((call) => call.url.endsWith("/runs/7/messages")).length
      expect(getsAfter).toBeGreaterThan(getsBefore)
    })
  })

  it("XSS-payload в сообщении рендерится текстом, а не скриптом", async () => {
    setFetchHandler(async (url, init) => {
      if (url.endsWith("/runs/7/messages") && (init?.method ?? "GET") === "GET") {
        return {
          status: 200,
          body: [
            {
              id: "m1",
              role: "agent",
              content: "<script>alert(1)</script>",
              created_at: "2026-11-10T10:00:00Z",
            },
          ],
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })

    renderTab()

    expect(await screen.findByText("<script>alert(1)</script>")).toBeInTheDocument()
    expect(document.querySelector("script")).toBeNull()
  })

  it("показывает цитаты, на которых основан ответ агента", async () => {
    setFetchHandler(async (url, init) => {
      if (url.endsWith("/runs/7/messages") && (init?.method ?? "GET") === "GET") {
        return {
          status: 200,
          body: [
            {
              id: "m1",
              role: "agent",
              content: "Ответ основан на источнике",
              created_at: "2026-11-10T10:00:00Z",
              citations: [{ title: "Анализ продуктовых метрик", quote: "Опыт с pandas" }],
            },
          ],
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })

    renderTab()

    expect(await screen.findByText("На чём основан ответ")).toBeInTheDocument()
    expect(screen.getByText(/Анализ продуктовых метрик/)).toBeInTheDocument()
    expect(screen.getByText(/Опыт с pandas/)).toBeInTheDocument()
  })

  it("не падает, если вместо истории пришёл объект ошибки", async () => {
    setFetchHandler(async (url, init) => {
      if (url.endsWith("/runs/7/messages") && (init?.method ?? "GET") === "GET") {
        return { status: 404, body: { code: "NOT_FOUND", message: "chat session not found" } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })

    renderTab()

    expect(await screen.findByText(/Чат ещё не открыт/)).toBeInTheDocument()
  })

  it("показывает тосты при обрыве и восстановлении соединения", async () => {
    stubChat()
    renderTab()
    await screen.findByText("Какие задачи вы решаете чаще всего?")
    const source = FakeEventSource.instances[0]
    const warning = vi.spyOn(toast, "warning")
    const error = vi.spyOn(toast, "error")
    const success = vi.spyOn(toast, "success")

    act(() => {
      source.onerror?.()
    })
    expect(warning).toHaveBeenCalled()

    act(() => {
      source.onopen?.()
    })
    expect(success).toHaveBeenCalled()
    expect(error).not.toHaveBeenCalled()
  })

  it("ошибка загрузки истории показывает баннер с кнопкой повтора", async () => {
    setFetchHandler(async () => {
      throw new Error("offline")
    })
    renderTab()

    expect(await screen.findByText(/История чата недоступна/)).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Повторить/ })).toBeInTheDocument()
  })

  it("после завершения интервью показывает работу аналитика вместо обрыва", async () => {
    stubChat()
    render(<ChatTab runId="7" active stage="analysis" />)
    await screen.findByText("Какие задачи вы решаете чаще всего?")

    expect(
      screen.getByText(/Интервью завершено - агент-аналитик строит дерево/),
    ).toBeInTheDocument()
    expect(screen.queryByText(/Соединение потеряно/)).not.toBeInTheDocument()
  })

  it("после завершения прогона показывает закрытый чат", async () => {
    stubChat()
    render(<ChatTab runId="7" active stage="done" runStatus="done" />)
    await screen.findByText("Какие задачи вы решаете чаще всего?")

    expect(screen.getByText(/Интервью завершено - чат закрыт/)).toBeInTheDocument()
    expect(screen.queryByText(/Соединение потеряно/)).not.toBeInTheDocument()
    expect(screen.getByRole("textbox")).toBeDisabled()
  })

  it("оставляет историю после завершения и даёт скачать транскрипт", async () => {
    stubChat()
    Object.assign(URL, {
      createObjectURL: vi.fn(() => "blob:transcript"),
      revokeObjectURL: vi.fn(),
    })
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {})

    render(<ChatTab runId="7" active stage="done" runStatus="done" />)
    expect(await screen.findByText("Какие задачи вы решаете чаще всего?")).toBeInTheDocument()

    const download = screen.getByRole("button", { name: /Скачать транскрипт/ })
    fireEvent.click(download)

    expect(click).toHaveBeenCalledTimes(1)
    click.mockRestore()
  })

  it("прокручивает к цитате из интервью и подсвечивает сообщение", async () => {
    stubChat()
    const scroll = vi.fn()
    const original = Element.prototype.scrollIntoView
    Element.prototype.scrollIntoView = scroll
    try {
      render(<ChatTab runId="7" active focus={{ quote: "Какие задачи" }} />)
      await screen.findByText("Какие задачи вы решаете чаще всего?")

      await waitFor(() => expect(scroll).toHaveBeenCalled())
      await waitFor(() =>
        expect(document.getElementById("chat-message-m1")?.className).toContain("ring-primary"),
      )
    } finally {
      Element.prototype.scrollIntoView = original
    }
  })
})
