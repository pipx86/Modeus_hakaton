import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { describe, expect, it } from "vitest"
import { setFetchHandler } from "@/test/fetch-stub"
import { CompareRuns } from "@/pages/CompareRuns"

function stubCompare() {
  setFetchHandler(async (url) => {
    if (url.endsWith("/api/v1/runs")) {
      return {
        status: 200,
        body: [
          { id: "run-a", profession: "Data Scientist A", status: "done" },
          { id: "run-b", profession: "Data Scientist B", status: "done" },
          { id: "run-c", profession: "Ещё не готов", status: "running" },
        ],
      }
    }
    if (url.endsWith("/api/v1/runs/run-a/tree")) {
      return {
        status: 200,
        body: [
          { id: "r1", type: "role", title: "Аналитик", evidence: [] },
          { id: "f1", type: "function", title: "Отчётность", parent_id: "r1", evidence: [] },
        ],
      }
    }
    if (url.endsWith("/api/v1/runs/run-b/tree")) {
      return {
        status: 200,
        body: [
          { id: "r1", type: "role", title: "Аналитик данных", evidence: [] },
          { id: "f1", type: "function", title: "Отчётность", parent_id: "r1", evidence: [] },
          { id: "t1", type: "task", title: "Новая задача", parent_id: "f1", evidence: [] },
        ],
      }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/compare"]}>
      <CompareRuns />
    </MemoryRouter>,
  )
}

describe("CompareRuns", () => {
  it("сравнивает два завершённых прогона и показывает diff", async () => {
    stubCompare()
    const user = userEvent.setup()
    renderPage()

    await screen.findByRole("heading", { name: "Сравнение прогонов" })

    await user.click(screen.getByRole("combobox", { name: "Базовый прогон" }))
    await user.click(await screen.findByRole("option", { name: "Data Scientist A" }))
    await user.click(screen.getByRole("combobox", { name: "Новый прогон" }))
    await user.click(await screen.findByRole("option", { name: "Data Scientist B" }))

    expect(await screen.findByText("+1 добавлено")).toBeInTheDocument()
    expect(screen.getByText("-0 удалено")).toBeInTheDocument()
    expect(screen.getByText("~1 изменено")).toBeInTheDocument()
    expect(screen.getByText("Новая задача")).toBeInTheDocument()
    expect(screen.getByText(/Аналитик.*Аналитик данных/)).toBeInTheDocument()
  })

  it("просит минимум два завершённых прогона", async () => {
    setFetchHandler(async () => ({
      status: 200,
      body: [{ id: "run-a", profession: "Только один", status: "done" }],
    }))
    renderPage()

    expect(await screen.findByText(/минимум два завершённых/)).toBeInTheDocument()
  })
})
