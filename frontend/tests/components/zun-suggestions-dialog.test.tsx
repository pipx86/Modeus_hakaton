import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { ZunSuggestionsDialog } from "@/components/zun-suggestions-dialog"

const suggestion = {
  node_id: "n1",
  node_title: "Анализ метрик",
  knowledge: ["Метрики продукта"],
  skills: ["Считать retention"],
  abilities: ["Замечать аномалии"],
  source: "rule" as const,
  current_zun: null,
  can_apply: true,
}

describe("ZunSuggestionsDialog", () => {
  it("показывает предложения и применяет ЗУН", async () => {
    const onApplied = vi.fn()
    setFetchHandler(async (url, init) => {
      if (url.endsWith("/api/v1/runs/7/zun/suggestions")) {
        return { status: 200, body: [suggestion] }
      }
      if (url.endsWith("/api/v1/runs/7/zun/suggestions/apply") && init?.method === "POST") {
        return {
          status: 200,
          body: {
            run_id: "7",
            node_id: "n1",
            node_title: "Анализ метрик",
            source: "rule",
            applied: true,
            can_apply: false,
            current_zun: { knowledge: suggestion.knowledge, skills: [], abilities: [] },
          },
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    const user = userEvent.setup()
    render(
      <ZunSuggestionsDialog runId="7" open onOpenChange={() => {}} onApplied={onApplied} />,
    )

    expect(await screen.findByText("Анализ метрик")).toBeInTheDocument()
    expect(screen.getByText("Метрики продукта")).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Применить" }))

    await waitFor(() =>
      expect(
        fetchCalls().some(
          (call) =>
            call.url.endsWith("/api/v1/runs/7/zun/suggestions/apply") &&
            call.init.method === "POST",
        ),
      ).toBe(true),
    )
    expect(onApplied).toHaveBeenCalled()
  })

  it("сохраняет отказ от предложения", async () => {
    setFetchHandler(async (url, init) => {
      if (url.endsWith("/api/v1/runs/7/zun/suggestions")) {
        return { status: 200, body: [suggestion] }
      }
      if (url.endsWith("/api/v1/runs/7/zun/suggestions/reject") && init?.method === "POST") {
        return { status: 200, body: { run_id: "7", node_id: "n1", rejected: true } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    const user = userEvent.setup()
    render(<ZunSuggestionsDialog runId="7" open onOpenChange={() => {}} />)

    await user.click(await screen.findByRole("button", { name: "Скрыть предложение Анализ метрик" }))

    await waitFor(() =>
      expect(
        fetchCalls().some(
          (call) =>
            call.url.endsWith("/api/v1/runs/7/zun/suggestions/reject") &&
            call.init.method === "POST",
        ),
      ).toBe(true),
    )
    await waitFor(() =>
      expect(screen.queryByText("Анализ метрик")).not.toBeInTheDocument(),
    )
  })

  it("не даёт применить ЗУН, если он уже заполнен", async () => {
    setFetchHandler(async () => ({
      status: 200,
      body: [{ ...suggestion, can_apply: false, current_zun: { knowledge: ["x"], skills: [], abilities: [] } }],
    }))
    render(<ZunSuggestionsDialog runId="7" open onOpenChange={() => {}} />)

    expect(await screen.findByText("ЗУН уже заполнен")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Применить" })).toBeDisabled()
  })
})
