import { render, screen } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it } from "vitest"
import { setFetchHandler } from "@/test/fetch-stub"
import App from "@/App"
import { clearToken, storeToken } from "@/lib/auth"

function makeToken(payload: Record<string, unknown>): string {
  const encode = (value: unknown) =>
    window.btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
  return `${encode({ alg: "HS256", typ: "JWT" })}.${encode(payload)}.signature`
}

beforeEach(() => {
  window.history.pushState({}, "", "/")
  setFetchHandler(async (url) => {
    if (url.endsWith("/api/v1/runs")) return { status: 200, body: [] }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
})

afterEach(() => {
  clearToken()
})

describe("App", () => {
  it("рендерит оболочку с подзаголовком продукта", async () => {
    render(<App />)

    expect(await screen.findByText("Анализ деятельности")).toBeInTheDocument()
    expect(screen.getByRole("link", { name: "Исследования" })).toBeInTheDocument()
  })

  it("без токена перенаправляет на страницу входа", async () => {
    render(<App />)

    expect(await screen.findByRole("heading", { name: "Вход" })).toBeInTheDocument()
  })

  it("на корне показывает дашборд для авторизованного", async () => {
    storeToken(makeToken({ sub: "demo-admin", role: "admin", exp: 4102444800 }))

    render(<App />)

    expect(await screen.findByRole("heading", { name: "Исследования" })).toBeInTheDocument()
  })
})
