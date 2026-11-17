import { fireEvent, render, screen } from "@testing-library/react"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { describe, expect, it } from "vitest"
import { setFetchHandler } from "@/test/fetch-stub"
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
})
