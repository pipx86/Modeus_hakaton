import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { ConflictsTab } from "@/components/conflicts-tab"

const CONFLICT = {
  id: "c1",
  node_id: "node-1234567890",
  field: "title",
  values: [
    { source_id: "s-vacancy", source_type: "vacancy", value: "Аналитик", priority: 100 },
    { source_id: "s-interview", source_type: "interview", value: "Data Analyst", priority: 300 },
  ],
  winner: { source_id: "s-interview", source_type: "interview", value: "Data Analyst", priority: 300 },
  reason: "higher source priority wins: interview (300)",
}

function stubConflicts(body: unknown, status = 200) {
  return setFetchHandler(async (url) => {
    if (url.endsWith("/api/v1/runs/7/conflicts")) {
      return { status, body }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

describe("ConflictsTab", () => {
  it("показывает значения источников и победителя", async () => {
    stubConflicts([CONFLICT])
    render(<ConflictsTab runId="7" />)

    expect(await screen.findByText("Название")).toBeInTheDocument()
    expect(screen.getByText("Аналитик")).toBeInTheDocument()
    expect(screen.getByText("Data Analyst")).toBeInTheDocument()
    expect(screen.getByText("приоритет 300")).toBeInTheDocument()
    expect(screen.getByText("Предварительно выбрано - требуется проверка")).toBeInTheDocument()
    expect(screen.getByText(CONFLICT.reason)).toBeInTheDocument()
    expect(screen.getByText(/Найдено конфликтов: 1/)).toBeInTheDocument()
  })

  it("переходит к узлу дерева", async () => {
    stubConflicts([CONFLICT])
    const onFocusNode = vi.fn()
    render(<ConflictsTab runId="7" onFocusNode={onFocusNode} />)

    fireEvent.click(await screen.findByRole("button", { name: "Показать в дереве" }))

    expect(onFocusNode).toHaveBeenCalledWith(CONFLICT.node_id)
  })

  it("объясняет пустое состояние", async () => {
    stubConflicts([])
    render(<ConflictsTab runId="7" />)

    expect(await screen.findByText("Конфликтов не найдено")).toBeInTheDocument()
  })

  it("объясняет нехватку прав и даёт повторить", async () => {
    stubConflicts({ code: "FORBIDDEN", message: "forbidden" }, 403)
    render(<ConflictsTab runId="7" />)

    expect(
      await screen.findByText("Раздел доступен администратору."),
    ).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Повторить" }))
    await waitFor(() =>
      expect(
        fetchCalls().filter((call) => call.url.endsWith("/runs/7/conflicts")).length,
      ).toBeGreaterThan(1),
    )
  })
})
