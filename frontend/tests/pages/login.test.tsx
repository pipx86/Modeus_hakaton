import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { afterEach, describe, expect, it } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { Login } from "@/pages/login"
import { clearToken, getStoredToken, storeToken } from "@/lib/auth"

function makeToken(payload: Record<string, unknown>): string {
  const encode = (value: unknown) =>
    window.btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
  return `${encode({ alg: "HS256", typ: "JWT" })}.${encode(payload)}.signature`
}

function renderLogin() {
  return render(
    <MemoryRouter initialEntries={["/login"]}>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/" element={<div>home-page</div>} />
      </Routes>
    </MemoryRouter>,
  )
}

const token = makeToken({ sub: "user-1", role: "user", exp: 4102444800 })

describe("страница входа", () => {
  afterEach(() => {
    clearToken()
  })

  it("логинится по email и паролю", async () => {
    setFetchHandler(async (url) => {
      if (url.endsWith("/api/v1/auth/login")) {
        return {
          status: 200,
          body: {
            token,
            user: { id: "user-1", email: "user@example.com", display_name: "User", role: "user" },
          },
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    renderLogin()

    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "user@example.com" } })
    fireEvent.change(screen.getByLabelText("Пароль"), { target: { value: "password123" } })
    fireEvent.click(screen.getByRole("button", { name: "Войти" }))

    expect(await screen.findByText("home-page")).toBeInTheDocument()
    expect(getStoredToken()).toBe(token)
  })

  it("показывает ошибку неверных данных", async () => {
    setFetchHandler(async () => ({
      status: 401,
      body: { code: "UNAUTHORIZED", message: "неверный email или пароль" },
    }))
    renderLogin()

    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "user@example.com" } })
    fireEvent.change(screen.getByLabelText("Пароль"), { target: { value: "password123" } })
    fireEvent.click(screen.getByRole("button", { name: "Войти" }))

    expect(await screen.findByText("неверный email или пароль")).toBeInTheDocument()
    expect(getStoredToken()).toBeNull()
  })

  it("регистрирует аккаунт и отправляет display_name", async () => {
    setFetchHandler(async (url) => {
      if (url.endsWith("/api/v1/auth/register")) {
        return {
          status: 201,
          body: {
            token,
            user: { id: "user-1", email: "new@example.com", display_name: "Новый", role: "admin" },
          },
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    renderLogin()

    fireEvent.click(screen.getByRole("button", { name: "Регистрация" }))
    fireEvent.change(screen.getByLabelText("Имя"), { target: { value: "Новый" } })
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "new@example.com" } })
    fireEvent.change(screen.getByLabelText("Пароль"), { target: { value: "password123" } })
    fireEvent.change(screen.getByLabelText("Повторите пароль"), {
      target: { value: "password123" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Зарегистрироваться" }))

    expect(await screen.findByText("home-page")).toBeInTheDocument()
    const call = fetchCalls().find((item) => item.url.endsWith("/api/v1/auth/register"))
    expect(call).toBeDefined()
    const body = JSON.parse(String(call?.init.body)) as Record<string, string>
    expect(body.display_name).toBe("Новый")
    expect(body.email).toBe("new@example.com")
  })

  it("показывает сессию и очищает токен при выходе", async () => {
    storeToken(token)
    renderLogin()

    expect(screen.getByText("Вы вошли")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Выйти" }))
    await waitFor(() => expect(getStoredToken()).toBeNull())
    expect(screen.getByRole("button", { name: "Войти" })).toBeInTheDocument()
  })
})
