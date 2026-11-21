/**
 * Отчёт об изменениях деятельности между двумя исследованиями: выбор противника,
 * счётчики, группы «появилось/исчезло/изменилось», новые материалы и
 * нейтральные пустые состояния. 501 и 409 TREE_MISSING не показываются как
 * ошибки, прочие 4xx/5xx - сообщением с повтором. Сеть подменена fetch-stub.
 */
import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { ActivityChangesReport } from "@/components/activity-changes-report"
import type { ActivityChangeReport } from "@/lib/api/client"

const RUNS = [
  { id: "7", profession: "Аналитик данных", status: "done" },
  { id: "8", profession: "Инженер данных", status: "done" },
]

const REPORT: ActivityChangeReport = {
  run_id: "7",
  against_run_id: "8",
  same_profession: false,
  added: [
    {
      node_id: "n-added",
      node_title: "Готовить отчёт",
      node_type: "task",
      parent_title: "Анализ данных",
      evidence_count: 1,
      new_sources: [
        {
          source_id: "s-3",
          source_title: "Профстандарт аналитика",
          source_type: "standard",
        },
      ],
    },
  ],
  removed: [
    {
      node_id: "n-removed",
      node_title: "Устаревшая задача",
      node_type: "task",
      parent_title: null,
      evidence_count: 0,
      new_sources: [],
    },
  ],
  changed: [
    {
      node_id: "n-changed",
      node_title: "Собирать требования",
      node_type: "function",
      parent_title: "Аналитик данных",
      evidence_count: 2,
      new_sources: [],
      changed_fields: ["title", "description"],
    },
  ],
  summary: { added: 1, removed: 1, changed: 1, sources_added: 1, sources_removed: 0 },
}

interface StubOptions {
  runs?: unknown[]
  report?: ActivityChangeReport
  reportStatus?: number
  reportCode?: string
  runsStatus?: number
  runsMessage?: string
}

/** Обработчик списка исследований и отчёта об изменениях. */
function stubActivityChangesApi(options: StubOptions = {}) {
  setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.endsWith("/api/v1/runs") && method === "GET") {
      const status = options.runsStatus ?? 200
      if (status >= 400) {
        return { status, body: { code: "INTERNAL", message: options.runsMessage ?? "сломалось" } }
      }
      return { status: 200, body: options.runs ?? RUNS }
    }
    if (url.includes("/api/v1/runs/7/activity-changes") && method === "GET") {
      const status = options.reportStatus ?? 200
      if (status >= 400) {
        return {
          status,
          body: {
            code: options.reportCode ?? "INTERNAL",
            message: status === 409 ? "нет дерева" : "отчёт недоступен",
          },
        }
      }
      return { status: 200, body: options.report ?? REPORT }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

describe("ActivityChangesReport", () => {
  it("показывает счётчики, группы изменений и новые материалы", async () => {
    stubActivityChangesApi()
    render(<ActivityChangesReport runId="7" />)

    expect(await screen.findByText("Изменения деятельности при повторном исследовании"))
      .toBeInTheDocument()
    const select = await screen.findByLabelText("Исследование для сравнения")
    expect(select).toHaveValue("8")
    expect(within(select).getByRole("option", { name: /Инженер данных/ })).toBeInTheDocument()
    expect(within(select).queryByRole("option", { name: /Аналитик данных/ })).not.toBeInTheDocument()

    expect(await screen.findByText("Готовить отчёт")).toBeInTheDocument()
    expect(screen.getByText("Появилось: 1")).toBeInTheDocument()
    expect(screen.getByText("Исчезло: 1")).toBeInTheDocument()
    expect(screen.getByText("Изменилось: 1")).toBeInTheDocument()
    expect(screen.getByText("Новых материалов: 1")).toBeInTheDocument()

    // Новый материал добавленного узла.
    expect(screen.getByText("Профстандарт аналитика")).toBeInTheDocument()
    expect(screen.getByText("Профстандарт")).toBeInTheDocument()
    // Изменённые поля подписаны по-русски.
    expect(screen.getByText(/Изменённые поля: Название, Описание/)).toBeInTheDocument()
    // Разные профессии - пометка, а не ошибка.
    expect(screen.getByText(/Профессии исследований различаются/)).toBeInTheDocument()

    const removed = screen.getByRole("region", { name: "Исчезло" })
    expect(within(removed).getByText("Устаревшая задача")).toBeInTheDocument()
    expect(within(removed).getByText("Новых материалов нет.")).toBeInTheDocument()

    expect(
      fetchCalls().some((call) =>
        call.url.includes("/api/v1/runs/7/activity-changes?against=8"),
      ),
    ).toBe(true)
  })

  it("показывает нейтральное состояние, когда различий нет", async () => {
    stubActivityChangesApi({
      report: {
        ...REPORT,
        same_profession: true,
        added: [],
        removed: [],
        changed: [],
        summary: { added: 0, removed: 0, changed: 0, sources_added: 0, sources_removed: 0 },
      },
    })
    render(<ActivityChangesReport runId="7" />)

    expect(await screen.findByText("Различий не найдено")).toBeInTheDocument()
    expect(screen.queryByText(/Профессии исследований различаются/)).not.toBeInTheDocument()
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
  })

  it("объясняет, когда узлы те же, но материалы обновились", async () => {
    stubActivityChangesApi({
      report: {
        ...REPORT,
        same_profession: true,
        added: [],
        removed: [],
        changed: [],
        summary: { added: 0, removed: 0, changed: 0, sources_added: 2, sources_removed: 0 },
      },
    })
    render(<ActivityChangesReport runId="7" />)

    expect(await screen.findByText("Состав деятельности не изменился")).toBeInTheDocument()
    expect(
      screen.getByText(/обновились подтверждающие материалы: новых — 2/),
    ).toBeInTheDocument()
    expect(screen.getByText("Новых материалов: 2")).toBeInTheDocument()
  })

  it("без второго исследования показывает нейтральное пустое состояние", async () => {
    stubActivityChangesApi({ runs: [{ id: "7", profession: "Аналитик данных" }] })
    render(<ActivityChangesReport runId="7" />)

    expect(await screen.findByText("Пока не с чем сравнивать")).toBeInTheDocument()
    expect(
      fetchCalls().some((call) => call.url.includes("/activity-changes")),
    ).toBe(false)
  })

  it("на 409 TREE_MISSING показывает спокойное объяснение без ошибки", async () => {
    stubActivityChangesApi({ reportStatus: 409, reportCode: "TREE_MISSING" })
    render(<ActivityChangesReport runId="7" />)

    expect(await screen.findByText("Сравнивать пока нечего")).toBeInTheDocument()
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
  })

  it("на 501 показывает нейтральное пустое состояние", async () => {
    stubActivityChangesApi({ reportStatus: 501 })
    render(<ActivityChangesReport runId="7" />)

    expect(await screen.findByText("Отчёт пока недоступен")).toBeInTheDocument()
    expect(screen.getByText("Раздел пока не поддерживается сервером.")).toBeInTheDocument()
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
  })

  it("на 500 показывает видимую ошибку и повторяет загрузку", async () => {
    let calls = 0
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/runs") && method === "GET") {
        return { status: 200, body: RUNS }
      }
      if (url.includes("/api/v1/runs/7/activity-changes") && method === "GET") {
        calls += 1
        return calls === 1
          ? { status: 500, body: { code: "INTERNAL", message: "отчёт недоступен" } }
          : { status: 200, body: REPORT }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ActivityChangesReport runId="7" />)

    expect(await screen.findByText("отчёт недоступен")).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: "Повторить загрузку отчёта" }))
    expect(await screen.findByText("Готовить отчёт")).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument())
  })
})
