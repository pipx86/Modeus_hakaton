import { fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"
import { Login } from "@/pages/login"
import { clearToken, getStoredToken } from "@/lib/auth"

function makeToken(payload: Record<string, unknown>): string {
  const encode = (value: unknown) =>
    window.btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
  return `${encode({ alg: "HS256", typ: "JWT" })}.${encode(payload)}.signature`
}

function submitToken(token: string) {
  render(<Login />)
  fireEvent.change(screen.getByLabelText("Токен"), { target: { value: token } })
  fireEvent.click(screen.getByRole("button", { name: "Войти" }))
}

describe("страница входа", () => {
  afterEach(() => {
    clearToken()
  })

  it("принимает валидный токен и показывает сессию", () => {
    submitToken(makeToken({ sub: "local-demo", role: "admin", exp: 4102444800 }))

    expect(screen.getByText("Вы вошли")).toBeInTheDocument()
    expect(screen.getByText("local-demo")).toBeInTheDocument()
    expect(screen.getByText("admin")).toBeInTheDocument()
    expect(getStoredToken()).not.toBeNull()
  })

  it("показывает ошибку для мусора вместо токена", () => {
    submitToken("not-a-jwt")

    expect(screen.getByText(/Это не похоже на JWT/)).toBeInTheDocument()
    expect(getStoredToken()).toBeNull()
  })

  it("не принимает просроченный токен", () => {
    submitToken(makeToken({ sub: "old", role: "student", exp: Math.floor(Date.now() / 1000) - 60 }))

    expect(screen.getByText(/Срок действия токена истёк/)).toBeInTheDocument()
    expect(getStoredToken()).toBeNull()
  })

  it("выход очищает сохранённый токен", () => {
    submitToken(makeToken({ sub: "local-demo", role: "admin", exp: 4102444800 }))
    expect(screen.getByText("Вы вошли")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Выйти" }))

    expect(screen.getByLabelText("Токен")).toBeInTheDocument()
    expect(getStoredToken()).toBeNull()
  })
})
