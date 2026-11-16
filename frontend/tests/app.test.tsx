import { render, screen } from "@testing-library/react"
import { beforeEach, describe, expect, it } from "vitest"
import { setFetchHandler } from "@/test/fetch-stub"
import App from "@/App"

beforeEach(() => {
  window.history.pushState({}, "", "/")
  setFetchHandler(async (url) => {
    if (url.endsWith("/api/v1/runs")) return { status: 200, body: [] }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
})

describe("App", () => {
  it("рендерит оболочку с подзаголовком продукта", async () => {
    render(<App />)

    expect(await screen.findByText("Анализ деятельности")).toBeInTheDocument()
    expect(screen.getByRole("link", { name: "Исследования" })).toBeInTheDocument()
  })

  it("на корне показывает дашборд", async () => {
    render(<App />)

    expect(await screen.findByRole("heading", { name: "Исследования" })).toBeInTheDocument()
  })
})
