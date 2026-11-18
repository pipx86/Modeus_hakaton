import { afterEach, describe, expect, it } from "vitest"
import {
  clearToken,
  decodeToken,
  getStoredToken,
  getValidToken,
  isTokenExpired,
  storeToken,
} from "@/lib/auth"

function makeToken(payload: Record<string, unknown>): string {
  const encode = (value: unknown) =>
    window.btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
  return `${encode({ alg: "HS256", typ: "JWT" })}.${encode(payload)}.signature`
}

describe("локальная JWT-сессия", () => {
  afterEach(() => {
    clearToken()
  })

  it("декодирует payload токена", () => {
    const token = makeToken({ sub: "local-demo", role: "admin", exp: 4102444800 })

    expect(decodeToken(token)).toMatchObject({ sub: "local-demo", role: "admin" })
  })

  it("возвращает null для мусора и неполного токена", () => {
    expect(decodeToken("not-a-token")).toBeNull()
    expect(decodeToken("a.b")).toBeNull()
    expect(decodeToken("a.%%%.c")).toBeNull()
  })

  it("определяет истёкший токен", () => {
    expect(isTokenExpired({ exp: Math.floor(Date.now() / 1000) - 60 })).toBe(true)
    expect(isTokenExpired({ exp: Math.floor(Date.now() / 1000) + 3600 })).toBe(false)
    expect(isTokenExpired({})).toBe(false)
    expect(isTokenExpired(null)).toBe(false)
  })

  it("хранит, отдаёт и очищает валидный токен", () => {
    const token = makeToken({ sub: "user", role: "teacher", exp: 4102444800 })

    storeToken(token)
    expect(getStoredToken()).toBe(token)
    expect(getValidToken()).toBe(token)

    clearToken()
    expect(getStoredToken()).toBeNull()
    expect(getValidToken()).toBeNull()
  })

  it("не отдаёт просроченный токен как валидный", () => {
    storeToken(makeToken({ sub: "user", exp: Math.floor(Date.now() / 1000) - 60 }))

    expect(getStoredToken()).not.toBeNull()
    expect(getValidToken()).toBeNull()
  })
})
