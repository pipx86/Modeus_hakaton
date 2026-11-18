/**
 * Вкладка «Снимки и проверка»: сравнение версий дерева и очередь спорных
 * подтверждений. Тесты работают через общий fetch-stub проекта и проверяют:
 * выбор двух последних снимков по умолчанию, отрисовку diff (поля «было/стало»),
 * диалог исходной цитаты с source_id/ref_id и переход к узлу дерева, а также
 * загрузку очереди, подтверждение/отклонение и ошибки доступа (403).
 */
import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { describe, expect, it, vi } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { SnapshotReviewTab } from "@/components/snapshot-review"
import type { ReviewQueueItem } from "@/lib/api/client"

const SNAPSHOTS = [
  { id: "snap-2", run_id: "7", version: 2, tree: { nodes: [] }, created_at: "2026-11-07T10:00:00Z" },
  { id: "snap-1", run_id: "7", version: 1, tree: { nodes: [] }, created_at: "2026-11-06T10:00:00Z" },
]

const SNAPSHOT_DIFF = {
  run_id: "7",
  base: { id: "snap-1", version: 1 },
  head: { id: "snap-2", version: 2 },
  added: [
    {
      id: "n-added",
      type: "task",
      title: "Новая задача",
      evidence: [
        {
          id: "e-added",
          node_id: "n-added",
          source_id: "src-1",
          ref_id: "p-7",
          quote: "Цитата добавленного узла",
          status: "pending",
        },
      ],
    },
  ],
  removed: [{ id: "n-removed", type: "action", title: "Удалённое действие", evidence: [] }],
  changed: [
    {
      id: "n-changed",
      before: {
        id: "n-changed",
        type: "function",
        title: "Старое название",
        description: "Старое описание",
        evidence: [],
      },
      after: {
        id: "n-changed",
        type: "function",
        title: "Новое название",
        description: "Новое описание",
        evidence: [
          {
            id: "e-changed",
            node_id: "n-changed",
            source_id: "src-1",
            ref_id: "p-1",
            quote: "Цитата изменённого узла",
            status: "pending",
          },
        ],
      },
      fields: ["title", "description", "evidence"],
    },
  ],
  summary: { added: 1, removed: 1, changed: 1 },
}

const PENDING_ITEM: ReviewQueueItem = {
  id: "ev-1",
  run_id: "7",
  node_id: "n-2",
  node_title: "Анализ метрик",
  source_id: "src-1",
  source_title: "Вакансия DS",
  source_url: "https://example.org/vacancy",
  ref_id: "p-3",
  quote: "Спорная цитата",
  status: "pending",
}

interface StubOptions {
  snapshots?: unknown[]
  diff?: unknown
  sources?: unknown[]
  queue?: ReviewQueueItem[]
  queueStatus?: number
  approveStatus?: number
}

/** Обработчик запросов вкладки: снимки, источники, очередь и мутации цитат. */
function stubReviewApi(options: StubOptions = {}) {
  return setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.includes("/diff?against=")) {
      return { status: 200, body: options.diff ?? SNAPSHOT_DIFF }
    }
    if (url.endsWith("/runs/7/snapshots") && method === "GET") {
      return { status: 200, body: options.snapshots ?? SNAPSHOTS }
    }
    if (url.endsWith("/api/v1/sources") && method === "GET") {
      return {
        status: 200,
        body: options.sources ?? [
          {
            id: "src-1",
            type: "vacancy",
            title: "Вакансия DS",
            content: "Требуется SQL",
            source_url: "https://example.org/vacancy",
          },
        ],
      }
    }
    if (url.includes("/api/v1/reviews/evidence")) {
      const status = options.queueStatus ?? 200
      if (status >= 400) {
        return { status, body: { code: "FORBIDDEN", message: "роль не подходит" } }
      }
      return { status: 200, body: options.queue ?? [] }
    }
    if (url.includes("/api/v1/evidences/") && method === "POST") {
      const status = options.approveStatus ?? 204
      if (status >= 400) {
        return { status, body: { code: "INTERNAL", message: "не удалось изменить статус" } }
      }
      return { status: 204, body: undefined }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

function renderTab(onFocusNode?: (nodeId: string) => void) {
  return render(
    <MemoryRouter>
      <SnapshotReviewTab runId="7" onFocusNode={onFocusNode} />
    </MemoryRouter>,
  )
}

describe("SnapshotReviewTab", () => {
  it("по умолчанию сравнивает два последних снимка и рисует diff", async () => {
    stubReviewApi()
    renderTab()

    const compareButton = await screen.findByRole("button", { name: "Сравнить снимки" })
    await waitFor(() => expect(compareButton).toBeEnabled())
    await userEvent.click(compareButton)

    expect(await screen.findByText("+1 добавлено")).toBeInTheDocument()
    expect(screen.getByText("-1 удалено")).toBeInTheDocument()
    expect(screen.getByText("~1 изменено")).toBeInTheDocument()
    expect(await screen.findByText("Новая задача")).toBeInTheDocument()
    expect(screen.getByText("Удалённое действие")).toBeInTheDocument()
    // Для изменённого узла показываются и старое, и новое значения.
    expect(screen.getAllByText("Старое название").length).toBeGreaterThan(0)
    expect(screen.getAllByText("Новое название").length).toBeGreaterThan(0)

    const diffCall = fetchCalls().find((call) => call.url.includes("/diff?against="))
    expect(diffCall?.url).toContain("/runs/7/snapshots/snap-2/diff?against=snap-1")
  })

  it("открывает диалог исходной цитаты и переходит к узлу дерева", async () => {
    const onFocusNode = vi.fn()
    stubReviewApi()
    renderTab(onFocusNode)

    const compareButton = await screen.findByRole("button", { name: "Сравнить снимки" })
    await waitFor(() => expect(compareButton).toBeEnabled())
    await userEvent.click(compareButton)

    const quoteButtons = await screen.findAllByRole("button", { name: /Открыть цитату/ })
    const addedQuote = quoteButtons.find((button) =>
      button.getAttribute("aria-label")?.includes("Цитата добавленного узла"),
    )
    expect(addedQuote).toBeDefined()
    await userEvent.click(addedQuote!)

    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).getByText(/Цитата добавленного узла/)).toBeInTheDocument()
    expect(within(dialog).getByText("src-1")).toBeInTheDocument()
    expect(within(dialog).getByText("p-7")).toBeInTheDocument()
    expect(within(dialog).getByText("Вакансия DS")).toBeInTheDocument()
    expect(within(dialog).getByRole("link", { name: /Открыть источник/ })).toHaveAttribute(
      "href",
      "https://example.org/vacancy",
    )

    await userEvent.click(within(dialog).getByRole("button", { name: "Показать в дереве" }))
    expect(onFocusNode).toHaveBeenCalledWith("n-added")
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
  })

  it("показывает пустое состояние, когда снимков ещё нет", async () => {
    stubReviewApi({ snapshots: [] })
    renderTab()

    expect(await screen.findByText("Снимков пока нет")).toBeInTheDocument()
  })

  it("загружает очередь проверки и подтверждает цитату", async () => {
    stubReviewApi({ queue: [PENDING_ITEM] })
    renderTab()

    expect(await screen.findByText(/Спорная цитата/)).toBeInTheDocument()
    expect(screen.getByText("Анализ метрик")).toBeInTheDocument()
    expect(screen.getByText("Источник: Вакансия DS")).toBeInTheDocument()

    const queueCall = fetchCalls().find((call) => call.url.includes("/reviews/evidence"))
    expect(queueCall?.url).toContain("status=pending")
    expect(queueCall?.url).toContain("limit=50")

    await userEvent.click(screen.getByRole("button", { name: "Подтвердить" }))

    await waitFor(() => {
      expect(
        fetchCalls().some(
          (call) =>
            call.url.includes("/api/v1/evidences/ev-1/approve") && call.init.method === "POST",
        ),
      ).toBe(true)
    })
    await waitFor(() => expect(screen.queryByText(/Спорная цитата/)).not.toBeInTheDocument())
    expect(screen.getByText("Нет цитат, ожидающих проверки.")).toBeInTheDocument()
  })

  it("оставляет строку очереди при ошибке мутации", async () => {
    stubReviewApi({ queue: [PENDING_ITEM], approveStatus: 500 })
    renderTab()

    await screen.findByText(/Спорная цитата/)
    await userEvent.click(screen.getByRole("button", { name: "Подтвердить" }))

    await waitFor(() =>
      expect(
        fetchCalls().some((call) => call.url.includes("/api/v1/evidences/ev-1/approve")),
      ).toBe(true),
    )
    expect(screen.getByText(/Спорная цитата/)).toBeInTheDocument()
  })

  it("переключает очередь на отклонённые цитаты", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/runs/7/snapshots") && method === "GET") {
        return { status: 200, body: SNAPSHOTS }
      }
      if (url.endsWith("/api/v1/sources") && method === "GET") {
        return { status: 200, body: [] }
      }
      if (url.includes("/api/v1/reviews/evidence")) {
        return {
          status: 200,
          body: url.includes("status=rejected")
            ? [{ ...PENDING_ITEM, id: "ev-2", quote: "Отклонённая цитата", status: "rejected" }]
            : [PENDING_ITEM],
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    renderTab()

    await screen.findByText(/Спорная цитата/)
    await userEvent.click(screen.getByRole("button", { name: "Отклонённые" }))

    expect(await screen.findByText(/Отклонённая цитата/)).toBeInTheDocument()
    const lastQueueCall = fetchCalls()
      .filter((call) => call.url.includes("/reviews/evidence"))
      .at(-1)
    expect(lastQueueCall?.url).toContain("status=rejected")
  })

  it("объясняет недоступность очереди для роли без прав", async () => {
    stubReviewApi({ queueStatus: 403 })
    renderTab()

    expect(await screen.findByText(/Недостаточно прав/)).toBeInTheDocument()
    // Сравнение снимков при этом продолжает работать.
    expect(screen.getByText("Сравнение снимков")).toBeInTheDocument()
  })
})
