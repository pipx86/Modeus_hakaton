import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { afterEach, describe, expect, it, vi } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { Admin } from "@/pages/admin"
import { clearToken, storeToken } from "@/lib/auth"

function makeToken(payload: Record<string, unknown>): string {
  const encode = (value: unknown) =>
    window.btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
  return `${encode({ alg: "HS256", typ: "JWT" })}.${encode(payload)}.signature`
}

function renderAdmin() {
  return render(
    <MemoryRouter>
      <Admin />
    </MemoryRouter>,
  )
}

function stubAdminData() {
  setFetchHandler(async (url) => {
    if (url.endsWith("/api/v1/users")) {
      return {
        status: 200,
        body: [
          { subject: "demo-admin", role: "admin" },
          { subject: "demo-guest", role: "guest", read_only: true },
        ],
      }
    }
    if (url.endsWith("/api/v1/runs")) {
      return {
        status: 200,
        body: [{ id: "1", profession: "Data Scientist", status: "done" }],
      }
    }
    if (url.endsWith("/api/v1/health")) {
      return { status: 200, body: { status: "ok" } }
    }
    if (url.endsWith("/api/v1/sources")) {
      return {
        status: 200,
        body: [{ id: "s-1", type: "document", title: "Профстандарт аналитика" }],
      }
    }
    if (url.includes("/api/v1/llm/usage")) {
      return {
        status: 200,
        body: {
          items: [
            {
              model: "minimax-test",
              requests: 3,
              prompt_tokens: 1200,
              completion_tokens: 800,
              total_tokens: 2000,
            },
          ],
        },
      }
    }
    if (url.includes("/api/v1/llm/requests")) {
      return {
        status: 200,
        body: {
          items: [
            {
              id: "llm-1",
              agent: "interviewer",
              model: "deepseek-test",
              status: "ok",
              latency_ms: 1200,
            },
          ],
        },
      }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

describe("админка", () => {
  afterEach(() => {
    clearToken()
  })

  it("не пускает пользователя без роли admin", () => {
    storeToken(makeToken({ sub: "demo-guest", role: "guest", exp: 4102444800 }))

    renderAdmin()

    expect(screen.getByText("Доступ только для администратора")).toBeInTheDocument()
  })

  it("показывает пользователей и исследования администратору", async () => {
    storeToken(makeToken({ sub: "demo-admin", role: "admin", exp: 4102444800 }))
    stubAdminData()

    renderAdmin()

    expect(await screen.findByText("demo-admin")).toBeInTheDocument()
    expect(await screen.findByText("demo-guest")).toBeInTheDocument()
    expect(await screen.findByText("Data Scientist")).toBeInTheDocument()
    expect(
      screen.getByRole("button", { name: /Удалить исследование Data Scientist/ }),
    ).toBeInTheDocument()
    expect((await screen.findAllByText("ok")).length).toBeGreaterThan(0)
    expect(screen.getByText("HTTP 5xx")).toBeInTheDocument()
    expect(screen.getByText("HTTP 4xx")).toBeInTheDocument()
    expect(screen.getByText("Токены по моделям")).toBeInTheDocument()
    expect(screen.getByText("Материалы для аналитика")).toBeInTheDocument()
    expect(await screen.findByText("Профстандарт аналитика")).toBeInTheDocument()
    expect(await screen.findByText("minimax-test")).toBeInTheDocument()
    expect(await screen.findByText("deepseek-test")).toBeInTheDocument()
  })

  it("загружает файл материала и удаляет его", async () => {
    storeToken(makeToken({ sub: "demo-admin", role: "admin", exp: 4102444800 }))
    let created = false

    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/users")) return { status: 200, body: [] }
      if (url.endsWith("/api/v1/runs")) return { status: 200, body: [] }
      if (url.endsWith("/api/v1/health")) return { status: 200, body: { status: "ok" } }
      if (url.includes("/api/v1/llm/usage")) return { status: 200, body: { items: [] } }
      if (url.includes("/api/v1/llm/requests")) return { status: 200, body: { items: [] } }
      if (url.endsWith("/api/v1/sources") && method === "GET") {
        return {
          status: 200,
          body: created ? [{ id: "s-2", type: "document", title: "role" }] : [],
        }
      }
      if (url.endsWith("/api/v1/sources/upload") && method === "POST") {
        created = true
        return { status: 201, body: { id: "s-2", type: "document", title: "role" } }
      }
      if (url.includes("/api/v1/sources/s-2") && method === "DELETE") {
        created = false
        return { status: 204, body: null }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })

    renderAdmin()

    expect(await screen.findByText("Материалов пока нет.")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Загрузить файл" })).toBeDisabled()

    const file = new File(["# Роль\nТекст документа"], "role.md", { type: "text/markdown" })
    fireEvent.change(screen.getByLabelText("Файл"), { target: { files: [file] } })
    fireEvent.click(screen.getByRole("button", { name: "Загрузить файл" }))

    expect(await screen.findByText("role")).toBeInTheDocument()
    expect(fetchCalls().some((call) => call.url.endsWith("/api/v1/sources/upload"))).toBe(true)

    fireEvent.click(screen.getByRole("button", { name: "Удалить материал role" }))
    await waitFor(() => expect(screen.queryByText("role")).not.toBeInTheDocument())
  })

  it("фильтрует LLM-запросы по агенту", async () => {
    storeToken(makeToken({ sub: "demo-admin", role: "admin", exp: 4102444800 }))
    stubAdminData()
    const user = userEvent.setup()

    renderAdmin()
    await screen.findByText("demo-admin")

    await user.click(screen.getAllByRole("combobox")[0])
    await user.click(await screen.findByRole("option", { name: "Аналитик" }))

    await waitFor(() => {
      expect(
        fetchCalls().some((call) => call.url.includes("agent=analyst")),
      ).toBe(true)
    })
  })

  it("показывает ошибку, если расход токенов недоступен", async () => {
    storeToken(makeToken({ sub: "demo-admin", role: "admin", exp: 4102444800 }))
    setFetchHandler(async (url) => {
      if (url.endsWith("/api/v1/users")) return { status: 200, body: [] }
      if (url.endsWith("/api/v1/runs")) return { status: 200, body: [] }
      if (url.endsWith("/api/v1/health")) return { status: 200, body: { status: "ok" } }
      if (url.endsWith("/api/v1/sources")) return { status: 200, body: [] }
      if (url.includes("/api/v1/llm/usage")) {
        return { status: 500, body: { code: "INTERNAL", message: "нет данных" } }
      }
      if (url.includes("/api/v1/llm/requests")) return { status: 200, body: { items: [] } }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })

    renderAdmin()

    expect(
      await screen.findByText(/Не удалось загрузить расход токенов/),
    ).toBeInTheDocument()
    expect(screen.getByText("Токенов за 24 часа")).toBeInTheDocument()
  })

  it("удаляет исследование после подтверждения", async () => {
    storeToken(makeToken({ sub: "demo-admin", role: "admin", exp: 4102444800 }))
    let deleted = false
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/users")) return { status: 200, body: [] }
      if (url.endsWith("/api/v1/runs") && method === "GET") {
        return {
          status: 200,
          body: deleted ? [] : [{ id: "run-1", profession: "Data Scientist", status: "done" }],
        }
      }
      if (url.endsWith("/api/v1/runs/run-1") && method === "DELETE") {
        deleted = true
        return { status: 204, body: null }
      }
      if (url.endsWith("/api/v1/health")) return { status: 200, body: { status: "ok" } }
      if (url.endsWith("/api/v1/sources")) return { status: 200, body: [] }
      if (url.includes("/api/v1/llm/usage")) return { status: 200, body: { items: [] } }
      if (url.includes("/api/v1/llm/requests")) return { status: 200, body: { items: [] } }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })

    renderAdmin()
    await screen.findByText("Data Scientist")

    fireEvent.click(screen.getByRole("button", { name: "Удалить исследование Data Scientist" }))

    await waitFor(() => expect(screen.queryByText("Data Scientist")).not.toBeInTheDocument())
    expect(
      fetchCalls().some(
        (call) => call.url.endsWith("/api/v1/runs/run-1") && call.init.method === "DELETE",
      ),
    ).toBe(true)
  })

  it("обновляет данные по кнопке «Обновить»", async () => {
    storeToken(makeToken({ sub: "demo-admin", role: "admin", exp: 4102444800 }))
    stubAdminData()

    renderAdmin()
    await screen.findByText("demo-admin")
    const before = fetchCalls().filter((call) => call.url.includes("/llm/requests")).length

    fireEvent.click(screen.getByRole("button", { name: /Обновить/ }))

    await waitFor(() => {
      const after = fetchCalls().filter((call) => call.url.includes("/llm/requests")).length
      expect(after).toBeGreaterThan(before)
    })
  })

  it("фильтрует LLM-запросы по статусу", async () => {
    storeToken(makeToken({ sub: "demo-admin", role: "admin", exp: 4102444800 }))
    stubAdminData()
    const user = userEvent.setup()

    renderAdmin()
    await screen.findByText("demo-admin")

    await user.click(screen.getAllByRole("combobox")[1])
    await user.click(await screen.findByRole("option", { name: "error" }))

    await waitFor(() => {
      expect(fetchCalls().some((call) => call.url.includes("status=error"))).toBe(true)
    })
  })

  it("оставляет исследование при ошибке удаления", async () => {
    storeToken(makeToken({ sub: "demo-admin", role: "admin", exp: 4102444800 }))
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/users")) return { status: 200, body: [] }
      if (url.endsWith("/api/v1/runs") && method === "GET") {
        return { status: 200, body: [{ id: "run-1", profession: "Data Scientist", status: "done" }] }
      }
      if (url.endsWith("/api/v1/runs/run-1") && method === "DELETE") {
        return { status: 500, body: { code: "INTERNAL", message: "не удалилось" } }
      }
      if (url.endsWith("/api/v1/health")) return { status: 200, body: { status: "ok" } }
      if (url.endsWith("/api/v1/sources")) return { status: 200, body: [] }
      if (url.includes("/api/v1/llm/usage")) return { status: 200, body: { items: [] } }
      if (url.includes("/api/v1/llm/requests")) return { status: 200, body: { items: [] } }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })

    renderAdmin()
    await screen.findByText("Data Scientist")

    fireEvent.click(screen.getByRole("button", { name: "Удалить исследование Data Scientist" }))

    await waitFor(() =>
      expect(fetchCalls().some((call) => call.init.method === "DELETE")).toBe(true),
    )
    expect(screen.getByText("Data Scientist")).toBeInTheDocument()
  })

  it("разбирает Prometheus-метрики и скачивает их", async () => {
    storeToken(makeToken({ sub: "demo-admin", role: "admin", exp: 4102444800 }))
    const metrics = [
      'activity_descriptor_http_requests_total{route="/api/v1/runs",status="200"} 10',
      'activity_descriptor_http_requests_total{route="/api/v1/runs",status="404"} 2',
      'activity_descriptor_http_requests_total{route="/api/v1/runs",status="500"} 1',
      'activity_descriptor_http_request_duration_seconds_sum{route="/api/v1/runs"} 1.5',
      'activity_descriptor_http_request_duration_seconds_count{route="/api/v1/runs"} 13',
      'activity_descriptor_llm_requests_total{agent="analyst",status="ok"} 4',
    ].join("\n")

    setFetchHandler(async (url) => {
      if (url.endsWith("/metrics")) return { status: 200, body: metrics }
      if (url.endsWith("/api/v1/users")) return { status: 200, body: [] }
      if (url.endsWith("/api/v1/runs")) return { status: 200, body: [] }
      if (url.endsWith("/api/v1/health")) return { status: 200, body: { status: "ok" } }
      if (url.endsWith("/api/v1/sources")) return { status: 200, body: [] }
      if (url.includes("/api/v1/llm/usage")) return { status: 200, body: { items: [] } }
      if (url.includes("/api/v1/llm/requests")) return { status: 200, body: { items: [] } }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    const createObjectURL = vi.fn(() => "blob:metrics")
    const revokeObjectURL = vi.fn()
    Object.assign(URL, { createObjectURL, revokeObjectURL })
    const downloads: string[] = []
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      downloads.push(this.download)
    })

    renderAdmin()

    expect(await screen.findByText("/api/v1/runs")).toBeInTheDocument()
    expect(screen.getAllByText("15.4%").length).toBeGreaterThan(0)

    fireEvent.click(screen.getByRole("button", { name: /Скачать \/metrics/ }))
    expect(downloads).toContain("metrics.prom")
    expect(createObjectURL).toHaveBeenCalledTimes(1)
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:metrics")
  })
})
