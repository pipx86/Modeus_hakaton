import { render, screen } from "@testing-library/react"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { afterEach, describe, expect, it } from "vitest"
import { RequireAuth } from "@/components/require-auth"
import { clearToken, storeToken } from "@/lib/auth"

function makeToken(payload: Record<string, unknown>): string {
  const encode = (value: unknown) =>
    window.btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
  return `${encode({ alg: "HS256", typ: "JWT" })}.${encode(payload)}.signature`
}

function renderGuard() {
  return render(
    <MemoryRouter initialEntries={["/secret?tab=tree"]}>
      <Routes>
        <Route path="/login" element={<div>login-page</div>} />
        <Route
          path="/secret"
          element={
            <RequireAuth>
              <div>secret-page</div>
            </RequireAuth>
          }
        />
      </Routes>
    </MemoryRouter>,
  )
}

describe("обязательный вход", () => {
  afterEach(() => {
    clearToken()
  })

  it("без токена перенаправляет на вход", () => {
    renderGuard()

    expect(screen.getByText("login-page")).toBeInTheDocument()
    expect(screen.queryByText("secret-page")).not.toBeInTheDocument()
  })

  it("с действующим токеном пускает на страницу", () => {
    storeToken(makeToken({ sub: "demo-admin", role: "admin", exp: 4102444800 }))

    renderGuard()

    expect(screen.getByText("secret-page")).toBeInTheDocument()
  })

  it("с просроченным токеном возвращает на вход", () => {
    storeToken(makeToken({ sub: "demo-admin", role: "admin", exp: 1 }))

    renderGuard()

    expect(screen.getByText("login-page")).toBeInTheDocument()
  })
})
