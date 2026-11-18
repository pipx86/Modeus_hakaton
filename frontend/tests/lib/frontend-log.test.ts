import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { frontendLog, installFrontendLogHandlers } from "@/lib/frontend-log"

async function waitForLogCall() {
  await vi.waitFor(() => {
    expect(fetchCalls().some((item) => item.url === "/__frontend-log")).toBe(true)
  })
  return fetchCalls().filter((item) => item.url === "/__frontend-log")
}

describe("файловый лог фронтенда", () => {
  beforeEach(() => {
    vi.stubEnv("MODE", "development")
    vi.spyOn(console, "warn").mockImplementation(() => {})
    vi.spyOn(console, "error").mockImplementation(() => {})
    setFetchHandler(async () => ({ status: 204 }))
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllEnvs()
  })

  it("отправляет запись на dev-эндпоинт с уровнем и контекстом", async () => {
    frontendLog.error("boom", { status: 500 })
    const calls = await waitForLogCall()

    expect(calls[0].init.method).toBe("POST")
    const body = JSON.parse(String(calls[0].init.body)) as {
      level: string
      message: string
      context: { status: number }
    }
    expect(body.level).toBe("error")
    expect(body.message).toBe("boom")
    expect(body.context).toEqual({ status: 500 })
  })

  it("пишет уровень warn без падения при недоступном эндпоинте", () => {
    setFetchHandler(async () => {
      throw new Error("offline")
    })

    expect(() => frontendLog.warn("warning", { code: "W1" })).not.toThrow()
  })

  it("подписывается на глобальные ошибки окна", async () => {
    installFrontendLogHandlers()
    installFrontendLogHandlers() // повторная установка не дублирует обработчики

    window.dispatchEvent(new ErrorEvent("error", { message: "window boom" }))
    const calls = await waitForLogCall()

    const body = JSON.parse(String(calls[calls.length - 1].init.body)) as {
      message: string
      context: { message: string }
    }
    expect(body.message).toBe("uncaught error")
    expect(body.context.message).toBe("window boom")
  })
})
