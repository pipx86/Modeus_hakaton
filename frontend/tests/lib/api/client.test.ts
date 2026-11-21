/**
 * Проверка сгенерированного клиента: префикс /api/v1, сериализация тела запроса
 * и разбор не-2xx ответа в типизированную ошибку контракта.
 *
 * Вторая часть - raw-fetch обёртки для эндпоинтов вне OpenAPI-схемы (снимки,
 * сравнение, очередь проверки): URL, query-параметры, Bearer-токен и ошибка
 * с HTTP-статусом.
 */
import { afterEach, describe, expect, test } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import {
  ApiRequestError,
  PROGRAM_DRAFT_DOCX_MIME,
  apiClient,
  compareRunSnapshots,
  downloadProgramDraftDocx,
  listReviewEvidence,
  listRunSnapshots,
  parseContentDispositionFilename,
} from "@/lib/api/client"
import { clearToken, storeToken } from "@/lib/auth"

afterEach(() => {
  clearToken()
})

function makeToken(payload: Record<string, unknown>): string {
  const encode = (value: unknown) =>
    window.btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
  return `${encode({ alg: "HS256", typ: "JWT" })}.${encode(payload)}.signature`
}

describe("apiClient (контракт openapi.yaml)", () => {
  test("GET /runs уходит на baseUrl с префиксом /api/v1", async () => {
    setFetchHandler(() => ({ status: 200, body: [] }))
    const { data, error } = await apiClient.GET("/runs")
    expect(error).toBeUndefined()
    expect(data).toEqual([])
    const [call] = fetchCalls()
    expect(call.url.endsWith("/api/v1/runs")).toBe(true)
    expect(call.init.method).toBe("GET")
  })

  test("POST /runs сериализует тело контракта (только profession)", async () => {
    setFetchHandler(() => ({
      status: 201,
      body: { id: "run-1", status: "pending", profession: "Data Scientist" },
    }))
    const { data, error } = await apiClient.POST("/runs", {
      body: { profession: "Data Scientist" },
    })
    expect(error).toBeUndefined()
    expect(data?.id).toBe("run-1")
    const [call] = fetchCalls()
    expect(call.url.endsWith("/api/v1/runs")).toBe(true)
    expect(call.init.method).toBe("POST")
    expect(JSON.parse(String(call.init.body))).toEqual({ profession: "Data Scientist" })
  })

  test("не-2xx ответ разбирается в типизированную ошибку Error{code,message}", async () => {
    setFetchHandler(() => ({
      status: 422,
      body: { code: "VALIDATION_FAILED", message: "profession не должна быть пустой" },
    }))
    const { data, error } = await apiClient.POST("/runs", { body: { profession: "" } })
    expect(data).toBeUndefined()
    expect(error?.code).toBe("VALIDATION_FAILED")
    expect(error?.message).toBe("profession не должна быть пустой")
  })
})

describe("raw-fetch обёртки вне OpenAPI-схемы", () => {
  test("compareRunSnapshots: head в пути, base в query against, id экранируются", async () => {
    setFetchHandler(() => ({
      status: 200,
      body: {
        run_id: "run-1",
        base: { id: "base-1", version: 1 },
        head: { id: "head-1", version: 2 },
        added: [],
        removed: [],
        changed: [],
        summary: { added: 0, removed: 0, changed: 0 },
      },
    }))
    const diff = await compareRunSnapshots("run-1", "head id", "base&id")
    expect(diff.summary).toEqual({ added: 0, removed: 0, changed: 0 })
    const [call] = fetchCalls()
    expect(call.url).toContain("/api/v1/runs/run-1/snapshots/head%20id/diff?against=base%26id")
  })

  test("listReviewEvidence собирает status/limit/offset", async () => {
    setFetchHandler(() => ({ status: 200, body: [] }))
    const items = await listReviewEvidence("rejected", 25, 10)
    expect(items).toEqual([])
    const [call] = fetchCalls()
    expect(call.url).toContain(
      "/api/v1/reviews/evidence?status=rejected&limit=25&offset=10",
    )
  })

  test("listRunSnapshots передаёт Bearer-токен сессии", async () => {
    const token = makeToken({ sub: "demo", role: "teacher", exp: 4102444800 })
    storeToken(token)
    setFetchHandler(() => ({ status: 200, body: [] }))
    await listRunSnapshots("run-1")
    const [call] = fetchCalls()
    expect(new Headers(call.init.headers).get("Authorization")).toBe(`Bearer ${token}`)
  })

  test("ошибка raw-запроса сохраняет HTTP-статус и код контракта", async () => {
    setFetchHandler(() => ({
      status: 403,
      body: { code: "FORBIDDEN", message: "нет прав" },
    }))
    const error = await listReviewEvidence("pending").catch((reason: unknown) => reason)
    expect(error).toBeInstanceOf(ApiRequestError)
    expect(error).toMatchObject({ status: 403, code: "FORBIDDEN", message: "нет прав" })
  })
})

describe("downloadProgramDraftDocx (DOCX черновика)", () => {
  test("разбирает имя из Content-Disposition: кавычки, без кавычек и filename*", () => {
    expect(parseContentDispositionFilename('attachment; filename="program-1.docx"')).toBe(
      "program-1.docx",
    )
    expect(parseContentDispositionFilename("attachment; filename=program-2.docx")).toBe(
      "program-2.docx",
    )
    expect(
      parseContentDispositionFilename("attachment; filename*=UTF-8''program-%D0%9F.docx"),
    ).toBe("program-П.docx")
    expect(parseContentDispositionFilename(null)).toBeNull()
    expect(parseContentDispositionFilename('attachment; filename=""')).toBeNull()
  })

  test("возвращает blob и fallback-имя, если заголовка нет", async () => {
    const file = new Blob(["docx"])
    setFetchHandler(() => ({
      status: 200,
      body: file,
      headers: { "content-type": PROGRAM_DRAFT_DOCX_MIME },
    }))
    const result = await downloadProgramDraftDocx("draft 7")
    expect(result.blob).toBe(file)
    expect(result.filename).toBe("program-draft 7.docx")
    const [call] = fetchCalls()
    expect(call.url.endsWith("/api/v1/admin/programs/drafts/draft%207/export.docx")).toBe(true)
  })

  test("403 приходит как ApiRequestError со статусом и кодом", async () => {
    setFetchHandler(() => ({
      status: 403,
      body: { code: "FORBIDDEN", message: "admin only" },
    }))
    const error = await downloadProgramDraftDocx("draft-1").catch((reason: unknown) => reason)
    expect(error).toBeInstanceOf(ApiRequestError)
    expect(error).toMatchObject({ status: 403, code: "FORBIDDEN" })
  })
})
