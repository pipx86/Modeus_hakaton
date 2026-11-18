import { render, screen } from "@testing-library/react"
import { MemoryRouter } from "react-router-dom"
import { afterEach, describe, expect, it } from "vitest"
import { setFetchHandler } from "@/test/fetch-stub"
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
    expect(await screen.findByText("deepseek-test")).toBeInTheDocument()
  })
})
