import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { describe, expect, it, vi } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { Research } from "@/pages/Research"
import { clearToken, storeToken } from "@/lib/auth"

function renderPage(runId = "7", search = "") {
  return render(
    <MemoryRouter initialEntries={[`/research/${runId}${search}`]}>
      <Routes>
        <Route path="/research/:id" element={<Research />} />
      </Routes>
    </MemoryRouter>,
  )
}

function makeUserToken(): string {
  const encode = (value: unknown) =>
    window.btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
  return `${encode({ alg: "HS256", typ: "JWT" })}.${encode({
    sub: "user-1",
    role: "user",
    exp: 4102444800,
  })}.signature`
}

function stubRun(
  overrides: {
    tree?: unknown[]
    treeResponse?: unknown
    sources?: unknown[]
    evidences?: unknown[]
    snapshots?: unknown[]
    reviewQueue?: unknown[]
    conflicts?: unknown[]
    deleteError?: boolean
    nodePatchError?: boolean
    approveError?: boolean
    createEvidenceError?: boolean
  } = {},
) {
  return setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.includes("/api/v1/sources/") && method === "DELETE") {
      return overrides.deleteError
        ? { status: 409, body: { code: "CONFLICT", message: "источник используется" } }
        : { status: 204, body: undefined }
    }
    if (url.endsWith("/api/v1/evidences") && method === "POST") {
      if (overrides.createEvidenceError) {
        return { status: 422, body: { code: "VALIDATION", message: "плохая цитата" } }
      }
      const body = JSON.parse(String(init.body)) as { source_id: string; quote: string }
      return {
        status: 201,
        body: { id: "e-new", source_id: body.source_id, quote: body.quote, approved: false },
      }
    }
    if (url.includes("/api/v1/nodes/") && method === "PATCH") {
      return overrides.nodePatchError
        ? { status: 422, body: { code: "VALIDATION", message: "плохое название" } }
        : { status: 200, body: {} }
    }
    if (url.includes("/api/v1/evidences/") && method === "POST") {
      return overrides.approveError
        ? { status: 500, body: { code: "INTERNAL", message: "не удалось" } }
        : { status: 200, body: {} }
    }
    if (url.includes("/api/v1/evidences/") && method === "PATCH") {
      const body = JSON.parse(String(init.body)) as { quote?: string }
      return {
        status: 200,
        body: { id: "e1", source_id: "s-1", quote: body.quote, status: "pending" },
      }
    }
    if (url.includes("/api/v1/evidences") && method === "GET") {
      return { status: 200, body: overrides.evidences ?? [] }
    }
    if (url.includes("/api/v1/reviews/evidence") && method === "GET") {
      return { status: 200, body: overrides.reviewQueue ?? [] }
    }
    if (url.includes("/api/v1/runs/7/export")) {
      return {
        status: 200,
        body: url.includes("format=json") ? '{"schema":"run-export/v1"}' : "# Экспорт интервью\n",
      }
    }
    if (url.endsWith("/api/v1/evidences/bulk-approve") && method === "POST") {
      const body = JSON.parse(String(init.body)) as { ids: string[] }
      return { status: 200, body: { approved: body.ids.length, skipped: 0 } }
    }
    if (url.endsWith("/api/v1/runs/7/sources") && method === "POST") {
      const body = JSON.parse(String(init.body)) as { type: string; title: string; content: string }
      return {
        status: 201,
        body: { id: "s-new", type: body.type, title: body.title, content: body.content },
      }
    }
    if (url.includes("/api/v1/runs/7/conflicts") && method === "GET") {
      return { status: 200, body: overrides.conflicts ?? [] }
    }
    if (url.includes("/api/v1/runs/7/snapshots") && method === "GET") {
      return { status: 200, body: overrides.snapshots ?? [] }
    }
    if (url.includes("/api/v1/runs/7/tree")) {
      return { status: 200, body: overrides.treeResponse ?? overrides.tree ?? [] }
    }
    if (url.includes("/api/v1/runs/7/messages")) {
      return { status: 200, body: [] }
    }
    if (url.includes("/api/v1/runs/7")) {
      return {
        status: 200,
        body: {
          id: "7",
          status: "done",
          profession: "Data Scientist",
          started_at: "2026-11-06T10:00:00Z",
        },
      }
    }
    if (url.endsWith("/api/v1/sources") && method === "GET") {
      return { status: 200, body: overrides.sources ?? [] }
    }
    if (url.endsWith("/api/v1/sources") && method === "POST") {
      const body = JSON.parse(String(init.body)) as { title: string }
      return {
        status: 201,
        body: { id: "s-new", type: "document", title: body.title, created_at: "2026-11-10T10:00:00Z" },
      }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

describe("Research", () => {
  it("при недоступном бэкенде показывает баннер и кнопку повтора", async () => {
    setFetchHandler(async () => {
      throw new Error("offline")
    })

    renderPage()

    expect(
      await screen.findByText("Бэкенд недоступен по адресу из VITE_API_URL"),
    ).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Повторить" })).toBeInTheDocument()
  })

  it("показывает профессию и список источников с сервера", async () => {
    stubRun({
      sources: [
        {
          id: "s-1",
          type: "vacancy",
          title: "Вакансия DS, HH",
          created_at: "2026-11-06T09:00:00Z",
        },
      ],
    })

    renderPage()

    expect(await screen.findByRole("heading", { name: "Data Scientist" })).toBeInTheDocument()
    expect(await screen.findByText("Вакансия DS, HH")).toBeInTheDocument()
  })

  it("показывает дату запуска", async () => {
    stubRun()

    renderPage()

    expect(await screen.findByText(/Запущено/)).toBeInTheDocument()
  })

  it("не показывает форму загрузки источников - только список", async () => {
    stubRun()

    renderPage()
    await screen.findByRole("heading", { name: "Data Scientist" })

    expect(screen.queryByRole("button", { name: "Добавить источник" })).not.toBeInTheDocument()
    expect(screen.queryByLabelText("Название")).not.toBeInTheDocument()
    expect(screen.queryByText("Добавить источник")).not.toBeInTheDocument()
  })

  it("пустое дерево показывает подсказку ожидания на вкладке «Дерево»", async () => {
    stubRun({ tree: [] })

    renderPage()
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "Data Scientist" })).toBeInTheDocument(),
    )

    // Radix Tabs (activationMode="automatic") выбирает вкладку по фокусу,
    // а jsdom не перемещает фокус при клике - поэтому focus(), не click().
    act(() => {
      screen.getByRole("tab", { name: "Дерево" }).focus()
    })

    expect(await screen.findByText("Дерево ещё не построено")).toBeInTheDocument()
  })

  it("берёт health из ответа /tree без отдельного /insights", async () => {
    stubRun({
      treeResponse: {
        nodes: [{ id: "r1", type: "role", title: "Data Scientist", evidence: [] }],
        insights: {
          run_id: "7",
          provenance: { nodes: {} },
          health: {
            score: 97,
            breakdown: { groundedness: 100, completeness: 90, duplicates: 100, orphans: 100 },
            weights: { groundedness: 0.4, completeness: 0.3, duplicates: 0.2, orphans: 0.1 },
            counts: {
              node_count: 1,
              grounded_nodes: 1,
              duplicate_nodes: 0,
              orphan_nodes: 0,
              childless_nodes: 0,
            },
            formula: "0.4*groundedness + ...",
          },
          flags: [],
          market_heat: { source: "none", top: [], nodes: {} },
          coverage: { columns: ["interview", "standard", "vacancy", "document"], rows: [] },
          sources: [],
          node_explanations: {},
        },
      },
    })

    renderPage("7", "?node=r1")

    expect(await screen.findByText("Здоровье: 97")).toBeInTheDocument()
    expect(fetchCalls().some((call) => call.url.includes("/runs/7/insights"))).toBe(false)
  })

  it("не показывает кнопку добавления стандарта или вакансии", async () => {
    stubRun({ sources: [] })
    renderPage()

    expect(await screen.findByText("Источники")).toBeInTheDocument()
    expect(
      screen.queryByRole("button", { name: "Добавить стандарт или вакансию" }),
    ).not.toBeInTheDocument()
  })

  it("на вкладке «Дерево» рисует узлы из API", async () => {
    stubRun({
      tree: [
        { id: "r1", type: "role", title: "Data Scientist", evidence: [] },
        {
          id: "f1",
          type: "function",
          title: "Анализ метрик",
          parent_id: "r1",
          evidence: [{ id: "e1", source_id: "s-1", quote: "Цитата" }],
        },
      ],
    })

    renderPage()
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "Data Scientist" })).toBeInTheDocument(),
    )

    act(() => {
      screen.getByRole("tab", { name: "Дерево" }).focus()
    })

    expect(await screen.findByText("Анализ метрик")).toBeInTheDocument()
  })

  it("подтверждает цитату из диалога обоснований", async () => {
    stubRun({
      tree: [
        { id: "r1", type: "role", title: "Data Scientist", evidence: [] },
        {
          id: "f1",
          type: "function",
          title: "Анализ метрик",
          parent_id: "r1",
          evidence: [{ id: "e1", source_id: "s-1", quote: "Цитата e1", approved: false }],
        },
      ],
    })

    renderPage()
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "Data Scientist" })).toBeInTheDocument(),
    )
    act(() => {
      screen.getByRole("tab", { name: "Дерево" }).focus()
    })

    fireEvent.click(
      await screen.findByRole("button", { name: "Узел Анализ метрик: показать обоснования" }),
    )
    fireEvent.click(await screen.findByRole("button", { name: "Подтвердить цитату" }))

    expect(await screen.findByText("Подтверждено")).toBeInTheDocument()
    const approve = fetchCalls().find(
      (call) => call.url.includes("/api/v1/evidences/e1/approve") && call.init.method === "POST",
    )
    expect(approve).toBeDefined()
  })

  it("переименовывает узел через диалог редактирования", async () => {
    stubRun({
      tree: [
        { id: "r1", type: "role", title: "Data Scientist", evidence: [] },
        { id: "f1", type: "function", title: "Анализ метрик", parent_id: "r1", evidence: [] },
      ],
    })

    renderPage()
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "Data Scientist" })).toBeInTheDocument(),
    )
    act(() => {
      screen.getByRole("tab", { name: "Дерево" }).focus()
    })
    await screen.findByText("Анализ метрик")

    fireEvent.click(screen.getByRole("button", { name: "Редактировать узел Анализ метрик" }))

    const dialog = await screen.findByRole("dialog")
    fireEvent.change(within(dialog).getByLabelText("Название"), { target: { value: "Новое имя" } })
    fireEvent.click(within(dialog).getByRole("button", { name: "Сохранить" }))

    await waitFor(() => {
      const patch = fetchCalls().find(
        (call) => call.url.includes("/api/v1/nodes/f1") && call.init.method === "PATCH",
      )
      expect(patch).toBeDefined()
      expect(JSON.parse(String(patch!.init.body))).toMatchObject({
        title: "Новое имя",
        type: "function",
      })
    })
  })

  it("прикрепляет цитату: создаёт evidence и патчит узел", async () => {
    stubRun({
      tree: [
        { id: "r1", type: "role", title: "Data Scientist", evidence: [] },
        { id: "f1", type: "function", title: "Анализ метрик", parent_id: "r1", evidence: [] },
      ],
      sources: [
        { id: "s-1", type: "vacancy", title: "Вакансия DS", created_at: "2026-11-06T09:00:00Z" },
      ],
    })

    renderPage()
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "Data Scientist" })).toBeInTheDocument(),
    )
    act(() => {
      screen.getByRole("tab", { name: "Дерево" }).focus()
    })
    await screen.findByText("Анализ метрик")

    fireEvent.click(
      screen.getByRole("button", { name: "Добавить обоснование к узлу Анализ метрик" }),
    )

    const dialog = await screen.findByRole("dialog")
    const user = userEvent.setup()
    await user.click(within(dialog).getByLabelText("Источник"))
    await user.click(await screen.findByRole("option", { name: "Вакансия DS" }))

    fireEvent.change(within(dialog).getByLabelText("Цитата"), {
      target: { value: "Цитата из вакансии" },
    })
    fireEvent.click(within(dialog).getByRole("button", { name: "Прикрепить" }))

    await waitFor(() => {
      const post = fetchCalls().find(
        (call) => call.url.endsWith("/api/v1/evidences") && call.init.method === "POST",
      )
      expect(post).toBeDefined()
      expect(JSON.parse(String(post!.init.body))).toEqual({
        node_id: "f1",
        source_id: "s-1",
        quote: "Цитата из вакансии",
      })
    })
  })

  it("показывает статус и confidence цитаты и умеет её отклонить", async () => {
    stubRun({
      sources: [
        { id: "s-1", type: "vacancy", title: "Вакансия DS", created_at: "2026-11-06T09:00:00Z" },
      ],
      evidences: [
        {
          id: "e1",
          source_id: "s-1",
          quote: "Цитата из вакансии",
          status: "pending",
          confidence: 0.82,
        },
      ],
    })

    renderPage()
    await screen.findByText("Вакансия DS")

    fireEvent.click(screen.getByRole("button", { name: "Показать цитаты" }))

    expect(await screen.findByText("На проверке")).toBeInTheDocument()
    expect(screen.getByText("82%")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Отклонить цитату из источника" }))

    expect(await screen.findByText("Отклонено")).toBeInTheDocument()
    const post = fetchCalls().find(
      (call) => call.url.includes("/api/v1/evidences/e1/reject") && call.init.method === "POST",
    )
    expect(post).toBeDefined()
  })

  it("правит текст цитаты через PATCH и обновляет её в панели", async () => {
    stubRun({
      sources: [
        { id: "s-1", type: "vacancy", title: "Вакансия DS", created_at: "2026-11-06T09:00:00Z" },
      ],
      evidences: [
        { id: "e1", source_id: "s-1", quote: "Старая цитата", status: "pending", confidence: 0.7 },
      ],
    })

    renderPage()
    await screen.findByText("Вакансия DS")
    fireEvent.click(screen.getByRole("button", { name: "Показать цитаты" }))
    await screen.findByText("Старая цитата")

    fireEvent.click(screen.getByRole("button", { name: "Редактировать цитату" }))

    const dialog = await screen.findByRole("dialog")
    const quoteField = within(dialog).getByLabelText("Цитата")
    fireEvent.change(quoteField, { target: { value: "Новая цитата" } })
    expect(quoteField).toHaveValue("Новая цитата")
    expect(within(dialog).getByRole("button", { name: "Сохранить" })).toBeEnabled()
    fireEvent.click(within(dialog).getByRole("button", { name: "Сохранить" }))

    await waitFor(() => {
      expect(
        fetchCalls().some(
          (call) => call.url.includes("/api/v1/evidences/e1") && call.init.method === "PATCH",
        ),
      ).toBe(true)
    })
    const patch = fetchCalls().find(
      (call) => call.url.includes("/api/v1/evidences/e1") && call.init.method === "PATCH",
    )
    expect(JSON.parse(String(patch!.init.body))).toEqual({ quote: "Новая цитата" })

    expect(await screen.findByText("Новая цитата")).toBeInTheDocument()
  })

  it("показывает содержимое источника в предпросмотре", async () => {
    stubRun({
      sources: [
        {
          id: "s-1",
          type: "vacancy",
          title: "Вакансия DS",
          content: "Требуется SQL и pandas",
          created_at: "2026-11-06T09:00:00Z",
        },
      ],
    })

    renderPage()
    await screen.findByText("Вакансия DS")

    fireEvent.click(screen.getByRole("button", { name: "Просмотреть источник" }))

    expect(await screen.findByText("Требуется SQL и pandas")).toBeInTheDocument()
  })

  it("удаляет источник и очищает его цитаты", async () => {
    stubRun({
      sources: [
        { id: "s-1", type: "vacancy", title: "Вакансия DS", created_at: "2026-11-06T09:00:00Z" },
      ],
      evidences: [{ id: "e1", source_id: "s-1", quote: "Цитата", status: "pending" }],
    })

    renderPage()
    await screen.findByText("Вакансия DS")
    fireEvent.click(screen.getByRole("button", { name: "Показать цитаты" }))
    await screen.findByText("Цитата")

    fireEvent.click(screen.getByRole("button", { name: "Удалить источник" }))
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Удалить" }))

    await waitFor(() => expect(screen.queryByText("Вакансия DS")).not.toBeInTheDocument())
    expect(
      fetchCalls().some(
        (call) => call.url.includes("/api/v1/sources/s-1") && call.init.method === "DELETE",
      ),
    ).toBe(true)
  })

  it("не удаляет источник при ошибке и оставляет его в списке", async () => {
    stubRun({
      deleteError: true,
      sources: [
        { id: "s-1", type: "vacancy", title: "Вакансия DS", created_at: "2026-11-06T09:00:00Z" },
      ],
    })

    renderPage()
    await screen.findByText("Вакансия DS")

    fireEvent.click(screen.getByRole("button", { name: "Удалить источник" }))
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Удалить" }))

    await waitFor(() =>
      expect(fetchCalls().some((call) => call.init.method === "DELETE")).toBe(true),
    )
    expect(screen.getByText("Вакансия DS")).toBeInTheDocument()
  })

  it("показывает пустое состояние панели цитат", async () => {
    stubRun({
      sources: [
        { id: "s-1", type: "vacancy", title: "Вакансия DS", created_at: "2026-11-06T09:00:00Z" },
      ],
    })

    renderPage()
    await screen.findByText("Вакансия DS")

    fireEvent.click(screen.getByRole("button", { name: "Показать цитаты" }))

    expect(
      await screen.findByText("Из этого источника пока не взято ни одной цитаты."),
    ).toBeInTheDocument()
  })

  it("отменяет правку цитаты без запроса", async () => {
    stubRun({
      sources: [
        { id: "s-1", type: "vacancy", title: "Вакансия DS", created_at: "2026-11-06T09:00:00Z" },
      ],
      evidences: [{ id: "e1", source_id: "s-1", quote: "Старая цитата", status: "pending" }],
    })

    renderPage()
    await screen.findByText("Вакансия DS")
    fireEvent.click(screen.getByRole("button", { name: "Показать цитаты" }))
    await screen.findByText("Старая цитата")

    fireEvent.click(screen.getByRole("button", { name: "Редактировать цитату" }))
    const dialog = await screen.findByRole("dialog")
    fireEvent.click(within(dialog).getByRole("button", { name: "Отмена" }))

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    expect(fetchCalls().some((call) => call.init.method === "PATCH")).toBe(false)
    expect(screen.getByText("Старая цитата")).toBeInTheDocument()
  })

  it("показывает ошибку сохранения узла и не закрывает диалог", async () => {
    stubRun({
      nodePatchError: true,
      tree: [
        { id: "r1", type: "role", title: "Data Scientist", evidence: [] },
        { id: "f1", type: "function", title: "Анализ метрик", parent_id: "r1", evidence: [] },
      ],
    })

    renderPage()
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "Data Scientist" })).toBeInTheDocument(),
    )
    act(() => {
      screen.getByRole("tab", { name: "Дерево" }).focus()
    })
    await screen.findByText("Анализ метрик")

    fireEvent.click(screen.getByRole("button", { name: "Редактировать узел Анализ метрик" }))
    const dialog = await screen.findByRole("dialog")
    fireEvent.change(within(dialog).getByLabelText("Название"), { target: { value: "Новое имя" } })
    fireEvent.click(within(dialog).getByRole("button", { name: "Сохранить" }))

    await waitFor(() =>
      expect(
        fetchCalls().some(
          (call) => call.url.includes("/api/v1/nodes/f1") && call.init.method === "PATCH",
        ),
      ).toBe(true),
    )
    expect(screen.getByRole("dialog")).toBeInTheDocument()
  })

  it("показывает вкладку чата", async () => {
    stubRun()

    renderPage()

    expect(await screen.findByRole("tab", { name: "Чат" })).toBeInTheDocument()
  })

  it("открывает вкладку «Снимки и проверка» и грузит снимки с очередью", async () => {
    stubRun({
      snapshots: [
        { id: "snap-1", run_id: "7", version: 1, tree: {}, created_at: "2026-11-06T10:00:00Z" },
        { id: "snap-2", run_id: "7", version: 2, tree: {}, created_at: "2026-11-07T10:00:00Z" },
      ],
      reviewQueue: [],
    })

    renderPage()
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "Data Scientist" })).toBeInTheDocument(),
    )

    act(() => {
      screen.getByRole("tab", { name: "Снимки и проверка" }).focus()
    })

    expect(await screen.findByText("Сравнение снимков")).toBeInTheDocument()
    expect(await screen.findByText("Очередь проверки")).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Сравнить снимки" })).toBeEnabled(),
    )
    expect(
      fetchCalls().some((call) => call.url.endsWith("/api/v1/runs/7/snapshots")),
    ).toBe(true)
    expect(
      fetchCalls().some((call) => call.url.includes("/api/v1/reviews/evidence")),
    ).toBe(true)
  })

  it("в режиме просмотра показывает баннер и не даёт менять данные", async () => {
    stubRun()

    renderPage("7", "?view=readonly")

    expect(
      await screen.findByText("Режим просмотра: дерево и цитаты доступны без изменений."),
    ).toBeInTheDocument()
  })

  it("копирует ссылку для просмотра", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
    })
    stubRun()

    renderPage()
    await screen.findByRole("heading", { name: "Data Scientist" })
    fireEvent.click(screen.getByRole("button", { name: "Поделиться ссылкой" }))

    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1))
    expect(writeText.mock.calls[0][0]).toContain("/research/7?view=readonly")
  })

  it("вызывает печать страницы", async () => {
    const print = vi.fn()
    Object.defineProperty(window, "print", { value: print, writable: true })
    stubRun()

    renderPage()
    await screen.findByRole("heading", { name: "Data Scientist" })
    fireEvent.click(screen.getByRole("button", { name: "Печать" }))

    expect(print).toHaveBeenCalledTimes(1)
  })

  it("показывает ошибку загрузки дерева", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.includes("/api/v1/runs/7/tree")) {
        throw new Error("offline")
      }
      if (url.includes("/api/v1/runs/7/snapshots")) return { status: 200, body: [] }
      if (url.includes("/api/v1/reviews/evidence")) return { status: 200, body: [] }
      if (url.includes("/api/v1/runs/7/messages")) return { status: 200, body: [] }
      if (url.includes("/api/v1/runs/7")) {
        return {
          status: 200,
          body: { id: "7", status: "done", profession: "Data Scientist" },
        }
      }
      if (url.endsWith("/api/v1/sources") && method === "GET") return { status: 200, body: [] }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })

    renderPage()
    await screen.findByRole("heading", { name: "Data Scientist" })

    act(() => {
      screen.getByRole("tab", { name: "Дерево" }).focus()
    })

    expect(await screen.findByText("Дерево недоступно")).toBeInTheDocument()
  })

  it("печатает дерево из вкладки", async () => {
    const print = vi.fn()
    Object.defineProperty(window, "print", { value: print, writable: true })
    stubRun({
      tree: [{ id: "r", type: "role", title: "Data Scientist", evidence: [] }],
    })

    renderPage()
    await screen.findByRole("heading", { name: "Data Scientist" })
    act(() => {
      screen.getByRole("tab", { name: "Дерево" }).focus()
    })
    await screen.findByRole("button", { name: "Печать / PDF" })

    fireEvent.click(screen.getByRole("button", { name: "Печать / PDF" }))

    expect(print).toHaveBeenCalledTimes(1)
  })

  it("оставляет диалог цитаты открытым при ошибке сохранения", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.includes("/api/v1/evidences/e1") && method === "PATCH") {
        return { status: 422, body: { code: "VALIDATION", message: "плохая цитата" } }
      }
      if (url.includes("/api/v1/evidences") && method === "GET") {
        return {
          status: 200,
          body: [{ id: "e1", source_id: "s-1", quote: "Старая цитата", status: "pending" }],
        }
      }
      if (url.includes("/api/v1/runs/7/tree")) return { status: 200, body: [] }
      if (url.includes("/api/v1/runs/7/snapshots")) return { status: 200, body: [] }
      if (url.includes("/api/v1/reviews/evidence")) return { status: 200, body: [] }
      if (url.includes("/api/v1/runs/7/messages")) return { status: 200, body: [] }
      if (url.includes("/api/v1/runs/7")) {
        return { status: 200, body: { id: "7", status: "done", profession: "Data Scientist" } }
      }
      if (url.endsWith("/api/v1/sources") && method === "GET") {
        return {
          status: 200,
          body: [{ id: "s-1", type: "vacancy", title: "Вакансия DS", created_at: "2026-11-06T09:00:00Z" }],
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })

    renderPage()
    await screen.findByText("Вакансия DS")
    fireEvent.click(screen.getByRole("button", { name: "Показать цитаты" }))
    await screen.findByText("Старая цитата")

    fireEvent.click(screen.getByRole("button", { name: "Редактировать цитату" }))
    const dialog = await screen.findByRole("dialog")
    fireEvent.change(within(dialog).getByLabelText("Цитата"), {
      target: { value: "Новая цитата" },
    })
    fireEvent.click(within(dialog).getByRole("button", { name: "Сохранить" }))

    await waitFor(() => {
      expect(
        fetchCalls().some(
          (call) => call.url.includes("/api/v1/evidences/e1") && call.init.method === "PATCH",
        ),
      ).toBe(true)
    })
    expect(screen.getByRole("dialog")).toBeInTheDocument()
    expect(screen.getByText("Старая цитата")).toBeInTheDocument()
  })

  it("подтверждает все цитаты источника", async () => {
    stubRun({
      sources: [{ id: "s-1", type: "vacancy", title: "Вакансия DS" }],
      evidences: [
        { id: "e1", source_id: "s-1", quote: "Первая", status: "pending" },
        { id: "e2", source_id: "s-1", quote: "Вторая", status: "pending" },
      ],
    })

    renderPage()
    await screen.findByText("Вакансия DS")
    fireEvent.click(screen.getByRole("button", { name: "Показать цитаты" }))
    await screen.findByText("Первая")

    fireEvent.click(screen.getByRole("button", { name: "Подтвердить все" }))

    await waitFor(() => {
      const bulk = fetchCalls().find(
        (call) =>
          call.url.endsWith("/api/v1/evidences/bulk-approve") && call.init.method === "POST",
      )
      expect(bulk).toBeDefined()
      expect(JSON.parse(String(bulk!.init.body))).toEqual({ ids: ["e1", "e2"] })
    })
    expect(screen.getAllByText("Подтверждено").length).toBeGreaterThanOrEqual(2)
  })

  it("фильтрует цитаты по статусу", async () => {
    stubRun({
      sources: [{ id: "s-1", type: "vacancy", title: "Вакансия DS" }],
      evidences: [
        { id: "e1", source_id: "s-1", quote: "Ожидает", status: "pending" },
        { id: "e2", source_id: "s-1", quote: "Уже подтверждена", status: "approved" },
      ],
    })

    renderPage()
    await screen.findByText("Вакансия DS")
    fireEvent.click(screen.getByRole("button", { name: "Показать цитаты" }))
    await screen.findByText("Ожидает")

    fireEvent.click(screen.getByRole("button", { name: "Подтверждены" }))

    expect(await screen.findByText("Уже подтверждена")).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText("Ожидает")).not.toBeInTheDocument())
  })

  it("не меняет статус цитаты при ошибке подтверждения", async () => {
    stubRun({
      approveError: true,
      sources: [{ id: "s-1", type: "vacancy", title: "Вакансия DS" }],
      evidences: [{ id: "e1", source_id: "s-1", quote: "Спорная", status: "pending" }],
    })

    renderPage()
    await screen.findByText("Вакансия DS")
    fireEvent.click(screen.getByRole("button", { name: "Показать цитаты" }))
    await screen.findByText("Спорная")

    fireEvent.click(screen.getByRole("button", { name: /Подтвердить цитату/ }))

    await waitFor(() =>
      expect(fetchCalls().some((call) => call.url.includes("/approve"))).toBe(true),
    )
    expect(screen.getByText("Спорная")).toBeInTheDocument()
    expect(screen.getAllByText("На проверке").length).toBeGreaterThan(0)
  })

  it("оставляет диалог прикрепления при ошибке создания цитаты", async () => {
    stubRun({
      createEvidenceError: true,
      tree: [
        { id: "r1", type: "role", title: "Data Scientist", evidence: [] },
        { id: "f1", type: "function", title: "Анализ метрик", parent_id: "r1", evidence: [] },
      ],
      sources: [{ id: "s-1", type: "vacancy", title: "Вакансия DS" }],
    })

    renderPage()
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "Data Scientist" })).toBeInTheDocument(),
    )
    act(() => {
      screen.getByRole("tab", { name: "Дерево" }).focus()
    })
    await screen.findByText("Анализ метрик")

    fireEvent.click(screen.getByRole("button", { name: "Добавить обоснование к узлу Анализ метрик" }))
    const dialog = await screen.findByRole("dialog")
    const user = userEvent.setup()
    await user.click(within(dialog).getByLabelText("Источник"))
    await user.click(await screen.findByRole("option", { name: "Вакансия DS" }))
    fireEvent.change(within(dialog).getByLabelText("Цитата"), {
      target: { value: "Цитата из вакансии" },
    })
    fireEvent.click(within(dialog).getByRole("button", { name: "Прикрепить" }))

    await waitFor(() =>
      expect(
        fetchCalls().some(
          (call) => call.url.endsWith("/api/v1/evidences") && call.init.method === "POST",
        ),
      ).toBe(true),
    )
    expect(screen.getByRole("dialog")).toBeInTheDocument()
  })

  it("открывает вкладку «Конфликты» и показывает разрешённый конфликт", async () => {
    stubRun({
      conflicts: [
        {
          id: "c1",
          node_id: "f1",
          field: "title",
          values: [
            { source_id: "s1", source_type: "vacancy", value: "Аналитик", priority: 100 },
            {
              source_id: "s2",
              source_type: "interview",
              value: "Data Scientist",
              priority: 300,
            },
          ],
          winner: {
            source_id: "s2",
            source_type: "interview",
            value: "Data Scientist",
            priority: 300,
          },
          reason: "higher source priority wins: interview (300)",
        },
      ],
    })

    renderPage()
    await screen.findByRole("heading", { name: "Data Scientist" })
    act(() => {
      screen.getByRole("tab", { name: "Конфликты" }).focus()
    })

    expect(await screen.findByText("Выбрано")).toBeInTheDocument()
    expect(screen.getByText("Аналитик")).toBeInTheDocument()
    expect(
      fetchCalls().some((call) => call.url.includes("/runs/7/conflicts")),
    ).toBe(true)
  })

  it("скрывает админские вкладки покрытия и конфликтов для пользователя", async () => {
    storeToken(makeUserToken())
    stubRun()
    try {
      renderPage()
      expect(await screen.findByRole("tab", { name: "Дерево" })).toBeInTheDocument()
      expect(screen.queryByRole("tab", { name: "Покрытие" })).not.toBeInTheDocument()
      expect(screen.queryByRole("tab", { name: "Конфликты" })).not.toBeInTheDocument()
    } finally {
      clearToken()
    }
  })
})
