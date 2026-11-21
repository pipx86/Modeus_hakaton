import { fireEvent, render, screen } from "@testing-library/react"
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom"
import { afterEach, describe, expect, it } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { Consent } from "@/pages/consent"
import { clearToken, storeToken } from "@/lib/auth"

function makeToken(payload: Record<string, unknown>): string {
  const encode = (value: unknown) =>
    window.btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
  return `${encode({ alg: "HS256", typ: "JWT" })}.${encode(payload)}.signature`
}

const token = makeToken({ sub: "user-1", role: "user", exp: 4102444800 })

const DOCUMENT = {
  version: "v1",
  title: "Согласие",
  text: "Настоящий текст согласия.",
}

function LocationProbe() {
  const location = useLocation()
  return <div data-testid="location">{location.pathname}</div>
}

function renderConsent() {
  return render(
    <MemoryRouter initialEntries={["/consent"]}>
      <Routes>
        <Route path="/consent" element={<Consent />} />
        <Route path="/" element={<LocationProbe />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe("страница согласия", () => {
  afterEach(() => {
    clearToken()
  })

  it("рендерит заголовок, версию и текст документа из API", async () => {
    setFetchHandler(async (url) => {
      if (url.endsWith("/api/v1/consent/document")) return { status: 200, body: DOCUMENT }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    renderConsent()

    expect(await screen.findByText("Настоящий текст согласия.")).toBeInTheDocument()
    expect(
      screen.getByRole("heading", { name: "Согласие на обработку персональных данных" }),
    ).toBeInTheDocument()
    expect(screen.getByText("Версия документа: v1")).toBeInTheDocument()
    expect(fetchCalls().some((call) => call.url.endsWith("/api/v1/consent/status"))).toBe(false)

    fireEvent.click(screen.getByRole("button", { name: "Назад" }))
    expect(await screen.findByTestId("location")).toHaveTextContent("/")
  })

  it("показывает запасной текст, если документ недоступен", async () => {
    setFetchHandler(async () => ({
      status: 503,
      body: { code: "UNAVAILABLE", message: "документ недоступен" },
    }))
    renderConsent()

    expect(await screen.findByText(/Показан типовой текст согласия/)).toBeInTheDocument()
    expect(
      screen.getByText(/Обработка персональных данных выполняется/),
    ).toBeInTheDocument()
    expect(screen.getByText("Версия документа: v1")).toBeInTheDocument()
  })

  it("принимает согласие, шлёт POST и обновляет статус", async () => {
    storeToken(token)
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/consent/document")) return { status: 200, body: DOCUMENT }
      if (url.endsWith("/api/v1/consent/status") && method === "GET") {
        return {
          status: 200,
          body: { document_version: "v1", accepted: false, accepted_at: null },
        }
      }
      if (url.endsWith("/api/v1/consent") && method === "POST") {
        return {
          status: 200,
          body: {
            document_version: "v1",
            accepted: true,
            accepted_at: "2026-11-21T10:00:00Z",
          },
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    renderConsent()

    fireEvent.click(await screen.findByRole("button", { name: "Принять" }))

    expect(await screen.findByText("Согласие получено")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Принять" })).not.toBeInTheDocument()

    const call = fetchCalls().find(
      (item) => item.url.endsWith("/api/v1/consent") && item.init.method === "POST",
    )
    expect(call).toBeDefined()
    expect(JSON.parse(String(call?.init.body))).toEqual({ document_version: "v1" })
  })

  it("показывает, что согласие уже получено", async () => {
    storeToken(token)
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/consent/document")) return { status: 200, body: DOCUMENT }
      if (url.endsWith("/api/v1/consent/status") && method === "GET") {
        return {
          status: 200,
          body: {
            document_version: "v1",
            accepted: true,
            accepted_at: "2026-11-01T09:00:00Z",
          },
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    renderConsent()

    expect(await screen.findByText("Согласие получено")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Принять" })).not.toBeInTheDocument()
  })
})
