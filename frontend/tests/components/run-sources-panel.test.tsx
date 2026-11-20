/**
 * Блок «Состав использованных материалов»: список привязанных к исследованию
 * материалов, число обоснований, раскрытие обоснований из дерева, диалог
 * контекста ответа, пустые состояния и ошибки. Сеть подменена общим fetch-stub.
 */
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { RunSourcesPanel } from "@/components/run-sources-panel"
import type { ActivityNode, RunSource } from "@/lib/api/client"

const SOURCES: RunSource[] = [
  {
    id: "s-1",
    type: "interview",
    title: "Интервью с аналитиком",
    source_url: null,
    created_at: "2026-11-06T10:00:00Z",
    evidence_count: 2,
  },
  {
    id: "s-2",
    type: "standard",
    title: "Профстандарт аналитика",
    source_url: "https://example.org/standard",
    created_at: "2026-11-05T10:00:00Z",
    evidence_count: 1,
  },
]

const TREE: ActivityNode[] = [
  {
    id: "n-1",
    type: "task",
    title: "Собирать требования",
    evidence: [
      { id: "e-1", node_id: "n-1", source_id: "s-1", quote: "Каждую неделю собираю требования", status: "approved" },
      { id: "e-3", node_id: "n-1", source_id: "s-1", quote: "Требования согласую с заказчиком", status: "pending" },
    ],
  },
  {
    id: "n-2",
    type: "task",
    title: "Готовить отчёты",
    evidence: [
      { id: "e-2", node_id: "n-2", source_id: "s-2", quote: "Готовлю еженедельные отчёты", status: "pending" },
    ],
  },
]

const CONTEXT = {
  run_id: "7",
  evidence: {
    id: "e-1",
    node_id: "n-1",
    node_title: "Собирать требования",
    source_id: "s-1",
    source_title: "Интервью с аналитиком",
    quote: "Каждую неделю собираю требования",
    status: "approved",
  },
  messages: [
    {
      id: "m-1",
      role: "user",
      content: "Каждую неделю я собираю требования у заказчика.",
      sequence: 2,
      created_at: "2026-11-18T10:01:00Z",
    },
  ],
  match: {
    message_id: "m-1",
    sequence: 2,
    role: "user",
    strategy: "quote",
    contains_quote: true,
    quote: "Каждую неделю собираю требования",
  },
  note: "Совпадение по тексту цитаты",
}

interface StubOptions {
  sources?: RunSource[]
  tree?: ActivityNode[]
  status?: number
  message?: string
  treeStatus?: number
  treeMessage?: string
}

/** Обработчик материалов, дерева и контекста обоснования. */
function stubSourcesApi(options: StubOptions = {}) {
  setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.endsWith("/api/v1/runs/7/sources") && method === "GET") {
      const status = options.status ?? 200
      if (status >= 400) {
        return { status, body: { code: "INTERNAL", message: options.message ?? "сломалось" } }
      }
      return { status: 200, body: options.sources ?? SOURCES }
    }
    if (url.includes("/api/v1/runs/7/tree") && method === "GET") {
      const status = options.treeStatus ?? 200
      if (status >= 400) {
        return {
          status,
          body: { code: "INTERNAL", message: options.treeMessage ?? "дерево недоступно" },
        }
      }
      return { status: 200, body: options.tree ?? TREE }
    }
    if (url.includes("/api/v1/runs/7/evidence/") && url.endsWith("/context")) {
      return { status: 200, body: CONTEXT }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

describe("RunSourcesPanel", () => {
  it("показывает материалы, человекочитаемый тип, дату, число обоснований и ссылку", async () => {
    stubSourcesApi()
    render(<RunSourcesPanel runId="7" />)

    expect(await screen.findByText("Интервью с аналитиком")).toBeInTheDocument()
    expect(screen.getByText("Профстандарт аналитика")).toBeInTheDocument()
    expect(screen.getByText(/Интервью · 6 ноября 2026/)).toBeInTheDocument()
    expect(screen.getByText(/Профстандарт · 5 ноября 2026/)).toBeInTheDocument()
    expect(screen.getByText("Обоснований: 2")).toBeInTheDocument()
    expect(screen.getByText("ID: s-1")).toBeInTheDocument()

    const link = screen.getByRole("link", { name: /Открыть материал/ })
    expect(link).toHaveAttribute("href", "https://example.org/standard")
  })

  it("раскрывает только обоснования выбранного материала и открывает контекст ответа", async () => {
    const onFocusNode = vi.fn()
    stubSourcesApi()
    render(<RunSourcesPanel runId="7" onFocusNode={onFocusNode} />)

    await screen.findByText("Интервью с аналитиком")
    await userEvent.click(
      screen.getByRole("button", { name: "Показать обоснования: Интервью с аналитиком" }),
    )

    expect(await screen.findByText(/Каждую неделю собираю требования/)).toBeInTheDocument()
    expect(screen.getByText(/Требования согласую с заказчиком/)).toBeInTheDocument()
    expect(screen.getAllByText("Собирать требования").length).toBeGreaterThan(0)
    expect(screen.getByText("Подтверждено")).toBeInTheDocument()
    // Обоснования другого материала не подмешиваются.
    expect(screen.queryByText(/Готовлю еженедельные отчёты/)).not.toBeInTheDocument()
    expect(
      fetchCalls().filter((call) => call.url.endsWith("/api/v1/runs/7/tree")),
    ).toHaveLength(1)

    await userEvent.click(
      screen.getByRole("button", { name: /Открыть обоснование: Каждую неделю собираю требования/ }),
    )

    const dialog = await screen.findByRole("dialog")
    expect(dialog).toBeInTheDocument()
    expect(await screen.findByText("Ответ, откуда взята цитата")).toBeInTheDocument()
    expect(
      screen.getByText("Каждую неделю я собираю требования у заказчика."),
    ).toBeInTheDocument()
    expect(screen.getByText("Совпадение по тексту цитаты")).toBeInTheDocument()

    await userEvent.click(screen.getByRole("button", { name: "Показать в дереве" }))
    expect(onFocusNode).toHaveBeenCalledWith("n-1")
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
  })

  it("показывает пустое состояние, когда материалы ещё не привязаны", async () => {
    stubSourcesApi({ sources: [] })
    render(<RunSourcesPanel runId="7" />)

    expect(await screen.findByText("Источники ещё не привязаны")).toBeInTheDocument()
    expect(screen.queryByText(/Не удалось/)).not.toBeInTheDocument()
  })

  it("на 501 показывает нейтральное пустое состояние, а не ошибку", async () => {
    stubSourcesApi({ status: 501, message: "not implemented" })
    render(<RunSourcesPanel runId="7" />)

    expect(await screen.findByText("Источники ещё не привязаны")).toBeInTheDocument()
    expect(screen.getByText("Раздел пока не поддерживается сервером.")).toBeInTheDocument()
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
  })

  it("на 500 показывает видимую ошибку и повторяет загрузку", async () => {
    let calls = 0
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/runs/7/sources") && method === "GET") {
        calls += 1
        return calls === 1
          ? { status: 500, body: { code: "INTERNAL", message: "материалы недоступны" } }
          : { status: 200, body: SOURCES }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<RunSourcesPanel runId="7" />)

    expect(await screen.findByText("материалы недоступны")).toBeInTheDocument()
    expect(screen.queryByText("Источники ещё не привязаны")).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole("button", { name: "Повторить загрузку материалов" }))

    expect(await screen.findByText("Интервью с аналитиком")).toBeInTheDocument()
  })

  it("не падает, если вместо массива пришёл объект, и показывает пустое состояние", async () => {
    setFetchHandler(async (url, init) => {
      if (url.endsWith("/api/v1/runs/7/sources") && (init?.method ?? "GET") === "GET") {
        return { status: 200, body: { id: "7", status: "done" } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<RunSourcesPanel runId="7" />)

    expect(await screen.findByText("Источники ещё не привязаны")).toBeInTheDocument()
  })
})
