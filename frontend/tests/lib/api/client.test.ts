/**
 * Проверка сгенерированного клиента: префикс /api/v1, сериализация тела запроса
 * и разбор не-2xx ответа в типизированную ошибку контракта.
 */
import { describe, expect, test } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { apiClient } from "@/lib/api/client"

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
