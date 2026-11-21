import { act, cleanup, render, screen } from "@testing-library/react"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { afterEach, describe, expect, it } from "vitest"
import { setFetchHandler } from "@/test/fetch-stub"
import { ConsentBanner } from "@/components/consent-banner"
import { clearToken, storeToken } from "@/lib/auth"

function makeToken(payload: Record<string, unknown>): string {
  const encode = (value: unknown) =>
    window.btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
  return `${encode({ alg: "HS256", typ: "JWT" })}.${encode(payload)}.signature`
}

const token = makeToken({ sub: "user-1", role: "user", exp: 4102444800 })

async function renderBanner(path = "/") {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="*" element={<ConsentBanner />} />
      </Routes>
    </MemoryRouter>,
  )
  // Даём отработать запросу статуса, чтобы его setState попал в act.
  await act(async () => {
    await new Promise((resolve) => window.setTimeout(resolve, 0))
  })
}

function statusHandler(accepted: boolean) {
  return setFetchHandler(async (url) => {
    if (url.endsWith("/api/v1/consent/status")) {
      return {
        status: 200,
        body: { document_version: "v1", accepted, accepted_at: accepted ? "2026-11-01T09:00:00Z" : null },
      }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

describe("баннер согласия", () => {
  afterEach(() => {
    cleanup()
    clearToken()
  })

  it("напоминает о согласии и ведёт на /consent", async () => {
    storeToken(token)
    statusHandler(false)
    await renderBanner("/")

    expect(
      screen.getByText("Нужно согласие на обработку персональных данных"),
    ).toBeInTheDocument()
    expect(screen.getByRole("link", { name: "Открыть и принять" })).toHaveAttribute(
      "href",
      "/consent",
    )
  })

  it("не показывается, если согласие уже принято", async () => {
    storeToken(token)
    statusHandler(true)
    await renderBanner("/")

    expect(
      screen.queryByText("Нужно согласие на обработку персональных данных"),
    ).not.toBeInTheDocument()
  })

  it("скрыт на самой странице согласия", async () => {
    storeToken(token)
    statusHandler(false)
    await renderBanner("/consent")

    expect(
      screen.queryByText("Нужно согласие на обработку персональных данных"),
    ).not.toBeInTheDocument()
  })
})
