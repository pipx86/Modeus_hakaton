/**
 * Отчёт о покрытии программы деятельностью: счётчики, группы узлов по статусу,
 * компетенции программы, отдельный список предложений и нейтральные формулировки.
 * Ошибки 4xx/5xx показываются сообщением, 501 - нейтральным пустым состоянием.
 * Сеть подменена общим fetch-stub.
 */
import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { setFetchHandler } from "@/test/fetch-stub"
import { ProgramCoverageReport } from "@/components/program-coverage-report"
import type { ProgramCoverage } from "@/lib/api/client"

const COVERAGE: ProgramCoverage = {
  run_id: "7",
  program_id: "p-1",
  program_title: "Прикладная математика",
  nodes: [
    {
      node_id: "n-1",
      node_title: "Анализ метрик",
      node_type: "function",
      status: "confirmed",
      links: [
        {
          link_id: "l-1",
          competency_id: "c-1",
          competency_title: "Анализ данных",
          status: "approved",
          reason: "Связь согласована методистом",
        },
      ],
    },
    {
      node_id: "n-2",
      node_title: "Сбор требований",
      node_type: "task",
      status: "proposed",
      links: [
        {
          link_id: "l-2",
          competency_id: "c-2",
          competency_title: "Проектирование",
          status: "proposed",
          reason: "Похожие формулировки о требованиях",
        },
      ],
    },
    {
      node_id: "n-3",
      node_title: "Коммуникация с заказчиком",
      node_type: "action",
      status: "none",
      links: [],
    },
  ],
  competencies: [
    { competency_id: "c-1", competency_title: "Анализ данных", links_count: 2 },
    { competency_id: "c-9", competency_title: "Компетенция без связей", links_count: 0 },
  ],
  counts: { confirmed: 1, proposed: 1, none: 1, total: 3 },
}

interface StubOptions {
  coverage?: ProgramCoverage
  status?: number
  message?: string
}

/** Обработчик отчёта о покрытии. */
function stubCoverageApi(options: StubOptions = {}) {
  setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.endsWith("/api/v1/runs/7/program-coverage") && method === "GET") {
      const status = options.status ?? 200
      if (status >= 400) {
        return { status, body: { code: "INTERNAL", message: options.message ?? "сломалось" } }
      }
      return { status: 200, body: options.coverage ?? COVERAGE }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

describe("ProgramCoverageReport", () => {
  it("показывает счётчики, группы узлов, компетенции и отдельно предложения", async () => {
    stubCoverageApi()
    render(<ProgramCoverageReport runId="7" />)

    expect(await screen.findByText("Анализ метрик")).toBeInTheDocument()
    expect(screen.getByText("Прикладная математика")).toBeInTheDocument()
    expect(screen.getByText("Подтверждено: 1")).toBeInTheDocument()
    expect(screen.getByText("Предложено: 1")).toBeInTheDocument()
    expect(screen.getByText("Не сопоставлено: 1")).toBeInTheDocument()
    expect(screen.getByText("Всего узлов: 3")).toBeInTheDocument()

    // Подтверждённая и предложенная связи видны сразу.
    expect(screen.getByText("Связь согласована методистом")).toBeInTheDocument()
    expect(screen.getAllByText("Сбор требований").length).toBeGreaterThan(0)
    expect(
      screen.getAllByText("Похожие формулировки о требованиях").length,
    ).toBeGreaterThan(0)

    // Узел без связи скрыт в свёрнутой группе «Не сопоставлено».
    expect(screen.queryByText("Коммуникация с заказчиком")).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: /^Не сопоставлено/ }))
    expect(await screen.findByText("Коммуникация с заказчиком")).toBeInTheDocument()
    expect(
      screen.getByText(
        "Связь не установлена: узел не сопоставлен с элементами программы.",
      ),
    ).toBeInTheDocument()

    // Компетенции: только связанные с деятельностью.
    expect(screen.getAllByText("Анализ данных").length).toBeGreaterThan(0)
    expect(screen.getByText("Связей: 2")).toBeInTheDocument()
    expect(screen.queryByText("Компетенция без связей")).not.toBeInTheDocument()

    // Спорные предложения вынесены в отдельный блок.
    const proposals = screen.getByRole("region", {
      name: "Предложения, ожидающие решения",
    })
    expect(within(proposals).getByText(/Предложения, ожидающие решения/)).toHaveTextContent("1")
    expect(
      within(proposals).getByText("Похожие формулировки о требованиях"),
    ).toBeInTheDocument()

    // Нейтральные формулировки: отсутствие связи - не «пробел» программы.
    expect(screen.queryByText(/пробел/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/недостат/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/слаб/i)).not.toBeInTheDocument()
  })

  it("показывает нулевые счётчики и нейтральное пустое состояние", async () => {
    stubCoverageApi({
      coverage: {
        ...COVERAGE,
        nodes: [],
        competencies: [],
        counts: { confirmed: 0, proposed: 0, none: 0, total: 0 },
      },
    })
    render(<ProgramCoverageReport runId="7" />)

    expect(await screen.findByText("Отчёт о покрытии пока не сформирован")).toBeInTheDocument()
    expect(screen.getByText("Подтверждено: 0")).toBeInTheDocument()
    expect(screen.getByText("Предложено: 0")).toBeInTheDocument()
    expect(screen.getByText("Не сопоставлено: 0")).toBeInTheDocument()
    expect(screen.getByText("Всего узлов: 0")).toBeInTheDocument()
    expect(screen.queryByText(/пробел/i)).not.toBeInTheDocument()
  })

  it("на 501 показывает нейтральное пустое состояние без ошибки", async () => {
    stubCoverageApi({ status: 501, message: "not implemented" })
    render(<ProgramCoverageReport runId="7" />)

    expect(await screen.findByText("Отчёт о покрытии пока не сформирован")).toBeInTheDocument()
    expect(screen.getByText("Раздел пока не поддерживается сервером.")).toBeInTheDocument()
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
  })

  it("на 500 показывает видимую ошибку и повторяет загрузку", async () => {
    let calls = 0
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/runs/7/program-coverage") && method === "GET") {
        calls += 1
        return calls === 1
          ? { status: 500, body: { code: "INTERNAL", message: "отчёт недоступен" } }
          : { status: 200, body: COVERAGE }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramCoverageReport runId="7" />)

    expect(await screen.findByText("отчёт недоступен")).toBeInTheDocument()
    expect(screen.queryByText("Отчёт о покрытии пока не сформирован")).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole("button", { name: "Повторить загрузку отчёта" }))
    expect(await screen.findByText("Анализ метрик")).toBeInTheDocument()
  })

  it("не падает, если вместо объекта пришёл массив", async () => {
    setFetchHandler(async (url, init) => {
      if (url.endsWith("/api/v1/runs/7/program-coverage") && (init?.method ?? "GET") === "GET") {
        return { status: 200, body: [] }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramCoverageReport runId="7" />)

    expect(await screen.findByText("Отчёт о покрытии пока не сформирован")).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText("Всего узлов: 0")).toBeInTheDocument())
  })
})
