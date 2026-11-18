import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { describe, expect, it } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { Dashboard } from "@/pages/Dashboard"

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/"]}>
      <Routes>
        <Route path="/" element={<Dashboard />} />
        <Route path="/research/:id" element={<div>page-research</div>} />
      </Routes>
    </MemoryRouter>,
  )
}

describe("Dashboard", () => {
  it("показывает исследования с сервера", async () => {
    setFetchHandler(async (url) => {
      if (url.endsWith("/api/v1/runs")) {
        return {
          status: 200,
          body: [
            { id: "1", profession: "Data Scientist", status: "done", started_at: "2026-11-06T10:00:00Z" },
            { id: "2", profession: "Frontend Developer", status: "running", started_at: "2026-11-07T10:00:00Z" },
          ],
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })

    renderPage()

    expect(await screen.findByText("Data Scientist")).toBeInTheDocument()
    expect(screen.getByText("Frontend Developer")).toBeInTheDocument()
    expect(screen.getByText("Завершено")).toBeInTheDocument()
    expect(screen.getByText("В работе")).toBeInTheDocument()
    expect(screen.getByText("Активность за 14 дней")).toBeInTheDocument()
  })

  it("считает количество исследований и активных", async () => {
    setFetchHandler(async () => ({
      status: 200,
      body: [
        { id: "1", profession: "A", status: "done" },
        { id: "2", profession: "B", status: "running" },
        { id: "3", profession: "C", status: "pending" },
      ],
    }))

    renderPage()

    expect(await screen.findByText(/3 исследований/)).toBeInTheDocument()
    expect(screen.getByText(/1 в работе/)).toBeInTheDocument()
  })

  it("показывает пустое состояние, если исследований нет", async () => {
    setFetchHandler(async () => ({ status: 200, body: [] }))

    renderPage()

    expect(await screen.findByText("Исследований пока нет")).toBeInTheDocument()
    expect(
      screen.getByText(/Создайте первое исследование/),
    ).toBeInTheDocument()
  })

  it("при ошибке API показывает состояние ошибки и повторяет загрузку", async () => {
    let attempt = 0
    setFetchHandler(async (url) => {
      if (!url.endsWith("/api/v1/runs")) {
        return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
      }
      attempt += 1
      if (attempt === 1) throw new Error("offline")
      return {
        status: 200,
        body: [{ id: "1", profession: "Data Scientist", status: "done" }],
      }
    })

    renderPage()

    expect(await screen.findByText("Сервер недоступен")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Повторить" })).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Повторить" }))
    expect(await screen.findByText("Data Scientist")).toBeInTheDocument()
  })

  it("переходит на страницу исследования по клику", async () => {
    setFetchHandler(async () => ({
      status: 200,
      body: [{ id: "42", profession: "Data Scientist", status: "done" }],
    }))

    renderPage()
    fireEvent.click(await screen.findByText("Data Scientist"))

    expect(await screen.findByText("page-research")).toBeInTheDocument()
  })

  it("удаляет исследование после подтверждения", async () => {
    setFetchHandler(async (url, init) => {
      const method = init.method ?? "GET"
      if (url.endsWith("/api/v1/runs") && method === "GET") {
        return {
          status: 200,
          body: [{ id: "1", profession: "Data Scientist", status: "done" }],
        }
      }
      if (url.endsWith("/api/v1/runs/1") && method === "DELETE") {
        return { status: 204, body: null }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })

    renderPage()
    expect(await screen.findByText("Data Scientist")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: /Удалить исследование Data Scientist/ }))
    expect(await screen.findByText("Удалить исследование?")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Удалить" }))

    await waitFor(() => {
      expect(screen.queryByText("Data Scientist")).not.toBeInTheDocument()
    })
  })

  it("ведёт в пример последнего завершённого прогона", async () => {
    setFetchHandler(async (url) => {
      if (url.endsWith("/api/v1/runs")) {
        return {
          status: 200,
          body: [
            { id: "done-1", profession: "Data Scientist", status: "done" },
            { id: "run-2", profession: "Backend", status: "running" },
          ],
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })

    renderPage()

    const link = await screen.findByRole("link", { name: /Посмотреть пример/ })
    expect(link).toHaveAttribute("href", "/research/done-1")

    fireEvent.click(link)
    expect(await screen.findByText("page-research")).toBeInTheDocument()
  })

  it("не показывает пример, если завершённых прогонов нет", async () => {
    setFetchHandler(async () => ({
      status: 200,
      body: [{ id: "run-1", profession: "Backend", status: "running" }],
    }))

    renderPage()

    expect(await screen.findByText("Backend")).toBeInTheDocument()
    expect(screen.queryByRole("link", { name: /Посмотреть пример/ })).not.toBeInTheDocument()
  })

  it("оставляет исследование при отмене удаления", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/runs") && method === "GET") {
        return {
          status: 200,
          body: [{ id: "1", profession: "Data Scientist", status: "done" }],
        }
      }
      if (url.endsWith("/api/v1/runs/1") && method === "DELETE") {
        return { status: 204, body: null }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })

    renderPage()
    expect(await screen.findByText("Data Scientist")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Удалить исследование Data Scientist" }))
    fireEvent.click(await screen.findByRole("button", { name: "Отмена" }))

    await waitFor(() => expect(screen.queryByText("Удалить исследование?")).not.toBeInTheDocument())
    expect(screen.getByText("Data Scientist")).toBeInTheDocument()
  })

  it("оставляет исследование при ошибке удаления", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/runs") && method === "GET") {
        return { status: 200, body: [{ id: "1", profession: "Data Scientist", status: "done" }] }
      }
      if (url.endsWith("/api/v1/runs/1") && method === "DELETE") {
        return { status: 500, body: { code: "INTERNAL", message: "не удалилось" } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })

    renderPage()
    expect(await screen.findByText("Data Scientist")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Удалить исследование Data Scientist" }))
    fireEvent.click(await screen.findByRole("button", { name: "Удалить" }))

    await waitFor(() =>
      expect(fetchCalls().some((call) => call.init.method === "DELETE")).toBe(true),
    )
    expect(screen.getByText("Data Scientist")).toBeInTheDocument()
  })

  it("форматирует среднюю длительность в минутах", async () => {
    setFetchHandler(async () => ({
      status: 200,
      body: [
        {
          id: "1",
          profession: "Data Scientist",
          status: "done",
          started_at: "2026-11-06T10:00:00Z",
          finished_at: "2026-11-06T10:01:30Z",
        },
      ],
    }))

    renderPage()

    expect(await screen.findByText("1.5 мин")).toBeInTheDocument()
  })
})
