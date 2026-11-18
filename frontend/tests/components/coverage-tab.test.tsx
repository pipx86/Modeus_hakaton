import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { CoverageTab } from "@/components/coverage-tab"

function stubCoverage() {
  setFetchHandler(async (url) => {
    if (url.includes("/coverage?format=csv")) {
      return { status: 200, body: "node_id,title,type,interview,standard,vacancy,document\n" }
    }
    if (url.endsWith("/api/v1/runs/7/coverage")) {
      return {
        status: 200,
        body: {
          run_id: "7",
          columns: ["interview", "standard", "vacancy", "document"],
          rows: [
            {
              node_id: "r1",
              title: "Аналитик",
              type: "role",
              cells: { interview: 2, standard: 0, vacancy: 1, document: 0 },
            },
          ],
        },
      }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

describe("CoverageTab", () => {
  it("рисует матрицу и скачивает CSV", async () => {
    stubCoverage()
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {})
    render(<CoverageTab runId="7" />)

    expect(await screen.findByText("Аналитик")).toBeInTheDocument()
    expect(screen.getByText("Интервью")).toBeInTheDocument()
    expect(screen.getByText("2")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Скачать CSV" }))
    await waitFor(() =>
      expect(fetchCalls().some((call) => call.url.includes("/coverage?format=csv"))).toBe(true),
    )
    expect(click).toHaveBeenCalled()
    click.mockRestore()
  })

  it("показывает пустое состояние", async () => {
    setFetchHandler(async () => ({
      status: 200,
      body: { run_id: "7", columns: ["interview"], rows: [] },
    }))
    render(<CoverageTab runId="7" />)
    expect(await screen.findByText(/Матрица пуста/)).toBeInTheDocument()
  })
})
