/**
 * Блок проверки узла: статус, комментарий методиста и решение эксперта.
 * Тесты идут через общий fetch-stub и проверяют бейдж, тело запроса
 * `POST /nodes/{id}/review`, ошибки и readonly-режим.
 */
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { NodeReview } from "@/components/node-review"

function stubReview(options: { status?: number; message?: string; response?: unknown } = {}) {
  return setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.includes("/api/v1/nodes/n-1/review") && method === "POST") {
      const status = options.status ?? 200
      if (status >= 400) {
        return {
          status,
          body: { code: "INTERNAL", message: options.message ?? "не удалось сохранить" },
        }
      }
      return {
        status: 200,
        body:
          options.response ?? {
            id: "n-1",
            type: "function",
            title: "Анализ метрик",
            evidence: [],
            review_status: "approved",
            review_comment: "",
            reviewed_at: "2026-11-20T10:00:00Z",
          },
      }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

describe("NodeReview", () => {
  it("показывает «не проверен» и подтверждает узел с пустым комментарием", async () => {
    stubReview()
    const onReviewed = vi.fn()
    render(<NodeReview nodeId="n-1" onReviewed={onReviewed} />)

    expect(screen.getByText("не проверен")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Подтвердить" }))

    expect(await screen.findByText("подтверждён")).toBeInTheDocument()
    const post = fetchCalls().find(
      (call) => call.url.endsWith("/api/v1/nodes/n-1/review") && call.init.method === "POST",
    )
    expect(post).toBeDefined()
    expect(JSON.parse(String(post!.init.body))).toEqual({ status: "approved", comment: "" })
    expect(onReviewed).toHaveBeenCalledWith(
      expect.objectContaining({ id: "n-1", review_status: "approved" }),
    )
  })

  it("отклоняет узел и отправляет комментарий", async () => {
    setFetchHandler(async (url, init) => {
      if (url.includes("/nodes/n-1/review") && init.method === "POST") {
        return {
          status: 200,
          body: {
            id: "n-1",
            type: "function",
            title: "Анализ метрик",
            evidence: [],
            review_status: "rejected",
            review_comment: "Нет подтверждающей цитаты",
            reviewed_at: "2026-11-20T10:00:00Z",
          },
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<NodeReview nodeId="n-1" />)

    fireEvent.change(screen.getByLabelText("Комментарий к решению (необязательно)"), {
      target: { value: "Нет подтверждающей цитаты" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Отклонить" }))

    expect(await screen.findByText("отклонён")).toBeInTheDocument()
    expect(screen.getByText(/Нет подтверждающей цитаты/)).toBeInTheDocument()

    const post = fetchCalls().find(
      (call) => call.url.endsWith("/nodes/n-1/review") && call.init.method === "POST",
    )
    expect(JSON.parse(String(post!.init.body))).toEqual({
      status: "rejected",
      comment: "Нет подтверждающей цитаты",
    })
  })

  it("показывает ошибку 500 и оставляет прежний статус", async () => {
    stubReview({ status: 500, message: "внутренняя ошибка" })
    const onReviewed = vi.fn()
    render(<NodeReview nodeId="n-1" onReviewed={onReviewed} />)

    fireEvent.click(screen.getByRole("button", { name: "Подтвердить" }))

    expect(await screen.findByRole("alert")).toHaveTextContent("внутренняя ошибка")
    expect(screen.getByText("не проверен")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Подтвердить" })).toBeEnabled()
    expect(onReviewed).not.toHaveBeenCalled()
  })

  it("объясняет 403 при недостатке прав", async () => {
    stubReview({ status: 403, message: "forbidden" })
    render(<NodeReview nodeId="n-1" />)

    fireEvent.click(screen.getByRole("button", { name: "Отклонить" }))

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Недостаточно прав: проверять узлы может только методист.",
    )
  })

  it("в readonly показывает статус и комментарий методиста без кнопок", () => {
    stubReview()
    render(
      <NodeReview
        nodeId="n-1"
        readOnly
        review={{
          review_status: "approved",
          review_comment: "Формулировка согласована",
          reviewed_at: "2026-11-20T10:00:00Z",
        }}
      />,
    )

    expect(screen.getByText("подтверждён")).toBeInTheDocument()
    expect(screen.getByText(/Формулировка согласована/)).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Подтвердить" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Отклонить" })).not.toBeInTheDocument()
    expect(
      screen.queryByLabelText("Комментарий к решению (необязательно)"),
    ).not.toBeInTheDocument()
  })

  it("переживает ответ без поля review_status", async () => {
    stubReview({ response: { id: "n-1", type: "function", title: "Узел", evidence: [] } })
    const onReviewed = vi.fn()
    render(<NodeReview nodeId="n-1" onReviewed={onReviewed} />)

    fireEvent.click(screen.getByRole("button", { name: "Подтвердить" }))

    await waitFor(() => expect(onReviewed).toHaveBeenCalled())
    expect(onReviewed.mock.calls[0][0].review_status).toBe("approved")
    expect(screen.getByText("подтверждён")).toBeInTheDocument()
  })
})
