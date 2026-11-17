import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { describe, expect, it } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { Research } from "@/pages/Research"

function renderPage(runId = "7") {
  return render(
    <MemoryRouter initialEntries={[`/research/${runId}`]}>
      <Routes>
        <Route path="/research/:id" element={<Research />} />
      </Routes>
    </MemoryRouter>,
  )
}

function stubRun(overrides: { tree?: unknown[]; sources?: unknown[] } = {}) {
  return setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.endsWith("/api/v1/evidences") && method === "POST") {
      const body = JSON.parse(String(init.body)) as { source_id: string; quote: string }
      return {
        status: 201,
        body: { id: "e-new", source_id: body.source_id, quote: body.quote, approved: false },
      }
    }
    if (url.includes("/api/v1/nodes/") && method === "PATCH") {
      return { status: 200, body: {} }
    }
    if (url.includes("/api/v1/evidences/") && method === "POST") {
      return { status: 200, body: {} }
    }
    if (url.includes("/api/v1/runs/7/tree")) {
      return { status: 200, body: overrides.tree ?? [] }
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

  it("добавляет источник через форму и показывает его в списке", async () => {
    stubRun()

    renderPage()
    await screen.findByRole("heading", { name: "Data Scientist" })

    fireEvent.change(screen.getByLabelText("Название"), { target: { value: "Новый документ" } })
    fireEvent.click(screen.getByRole("button", { name: "Добавить источник" }))

    expect(await screen.findByText("Новый документ")).toBeInTheDocument()
    const post = fetchCalls().find(
      (call) => call.url.endsWith("/api/v1/sources") && call.init.method === "POST",
    )
    expect(post).toBeDefined()
    expect(JSON.parse(String(post!.init.body))).toMatchObject({
      type: "document",
      title: "Новый документ",
    })
  })

  it("пустое дерево показывает подсказку ожидания на вкладке «Дерево»", async () => {
    stubRun({ tree: [] })

    renderPage()
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "Data Scientist" })).toBeInTheDocument(),
    )

    // Radix Tabs (activationMode="automatic") выбирает вкладку по фокусу,
    // а jsdom не перемещает фокус при клике — поэтому focus(), не click().
    screen.getByRole("tab", { name: "Дерево" }).focus()

    expect(await screen.findByText("Дерево ещё не построено")).toBeInTheDocument()
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

    screen.getByRole("tab", { name: "Дерево" }).focus()

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
    screen.getByRole("tab", { name: "Дерево" }).focus()

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
    screen.getByRole("tab", { name: "Дерево" }).focus()
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
    screen.getByRole("tab", { name: "Дерево" }).focus()
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
        source_id: "s-1",
        quote: "Цитата из вакансии",
      })
    })
  })

  it("показывает вкладку чата", async () => {
    stubRun()

    renderPage()

    expect(await screen.findByRole("tab", { name: "Чат" })).toBeInTheDocument()
  })
})
