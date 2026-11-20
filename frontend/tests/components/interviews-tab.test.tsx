/**
 * Вкладка «Интервью»: статусы сессий, история выбранного интервью и блок
 * «Ответы, ставшие основаниями». Проверяет, что основания и сообщения разных
 * интервью не смешиваются, узел ведёт на вкладку дерева, а 501/ошибка не роняют
 * экран. Сеть подменена общим fetch-stub.
 */
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { InterviewsTab } from "@/components/interviews-tab"
import type { InterviewMessage, RunInterview } from "@/lib/api/client"

const INTERVIEWS: RunInterview[] = [
  {
    id: "i-1",
    run_id: "7",
    respondent_name: "Анна",
    respondent_role: "Аналитик",
    status: "completed",
    started_at: "2026-11-06T10:00:00Z",
    finished_at: "2026-11-06T10:30:00Z",
  },
  {
    id: "i-2",
    run_id: "7",
    respondent_name: "Борис",
    status: "active",
    started_at: "2026-11-07T10:00:00Z",
  },
]

const MESSAGES: Record<string, InterviewMessage[]> = {
  "i-1": [
    {
      id: "m-1",
      role: "agent",
      content: "Как вы собираете требования?",
      created_at: "2026-11-06T10:00:05Z",
    },
    {
      id: "m-2",
      role: "user",
      content: "Каждую неделю я собираю требования у заказчика.",
      created_at: "2026-11-06T10:01:00Z",
    },
  ],
  "i-2": [
    {
      id: "m-3",
      role: "user",
      content: "Я готовлю еженедельные отчёты для команды.",
      created_at: "2026-11-07T10:01:00Z",
    },
  ],
}

const EVIDENCE = {
  "i-1": [
    {
      evidence_id: "e-1",
      node_id: "n-1",
      node_title: "Собирать требования",
      node_type: "task",
      quote: "Требования собираю каждую неделю",
      source_id: "s-1",
      source_title: "Интервью с аналитиком",
      status: "approved",
    },
  ],
  "i-2": [
    {
      evidence_id: "e-2",
      node_id: "n-2",
      node_title: "Готовить отчёты",
      node_type: "task",
      quote: "Отчёты готовлю еженедельно",
      source_id: "s-2",
      source_title: "Интервью с тимлидом",
      status: "pending",
    },
  ],
} as const

interface StubOptions {
  evidenceStatus?: number
  evidenceMessage?: string
  failFirstEvidence?: boolean
}

/** Обработчик списка интервью, сообщений и оснований. */
function stubInterviewsApi(options: StubOptions = {}) {
  const evidenceCalls = new Map<string, number>()
  setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.endsWith("/api/v1/runs/7/interviews") && method === "GET") {
      return { status: 200, body: INTERVIEWS }
    }
    const messagesMatch = url.match(/\/interviews\/([^/]+)\/messages/)
    if (messagesMatch) {
      const interviewId = decodeURIComponent(messagesMatch[1])
      return { status: 200, body: MESSAGES[interviewId] ?? [] }
    }
    const evidenceMatch = url.match(/\/interviews\/([^/]+)\/evidence/)
    if (evidenceMatch) {
      const interviewId = decodeURIComponent(evidenceMatch[1])
      const status = options.evidenceStatus ?? 200
      if (status >= 400) {
        return { status, body: { code: "INTERNAL", message: options.evidenceMessage ?? "сломалось" } }
      }
      if (options.failFirstEvidence && interviewId === "i-1") {
        const count = (evidenceCalls.get(interviewId) ?? 0) + 1
        evidenceCalls.set(interviewId, count)
        if (count === 1) {
          return { status: 500, body: { code: "INTERNAL", message: "основания недоступны" } }
        }
      }
      return { status: 200, body: EVIDENCE[interviewId as keyof typeof EVIDENCE] ?? [] }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

describe("InterviewsTab", () => {
  it("показывает статусы сессий и основания выбранного интервью", async () => {
    const onFocusNode = vi.fn()
    stubInterviewsApi()
    render(<InterviewsTab runId="7" onFocusNode={onFocusNode} />)

    expect((await screen.findAllByText("Анна")).length).toBeGreaterThan(0)
    expect(screen.getAllByText("Завершено").length).toBeGreaterThan(0)
    expect(screen.getByText("Идёт")).toBeInTheDocument()

    expect(
      await screen.findByText("Каждую неделю я собираю требования у заказчика."),
    ).toBeInTheDocument()
    expect(screen.getByText(/Требования собираю каждую неделю/)).toBeInTheDocument()
    expect(screen.getByText("Собирать требования")).toBeInTheDocument()
    expect(screen.getByText("Подтверждено")).toBeInTheDocument()
    expect(screen.getByText("Источник: Интервью с аналитиком")).toBeInTheDocument()

    await userEvent.click(
      screen.getByRole("button", { name: "Перейти к узлу Собирать требования" }),
    )
    expect(onFocusNode).toHaveBeenCalledWith("n-1")
  })

  it("не смешивает основания и сообщения разных интервью", async () => {
    stubInterviewsApi()
    render(<InterviewsTab runId="7" />)

    await screen.findByText("Собирать требования")
    expect(screen.queryByText("Готовить отчёты")).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole("button", { name: "История интервью: Борис" }))

    expect(await screen.findByText("Готовить отчёты")).toBeInTheDocument()
    expect(screen.getByText("На проверке")).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.queryByText("Собирать требования")).not.toBeInTheDocument(),
    )
    expect(
      screen.queryByText("Каждую неделю я собираю требования у заказчика."),
    ).not.toBeInTheDocument()
    expect(
      screen.getByText("Я готовлю еженедельные отчёты для команды."),
    ).toBeInTheDocument()

    const evidenceCalls = fetchCalls().filter((call) =>
      call.url.includes("/interviews/i-2/evidence"),
    )
    expect(evidenceCalls.length).toBeGreaterThan(0)
  })

  it("на 501 по основаниям показывает нейтральное состояние, а не падение", async () => {
    stubInterviewsApi({ evidenceStatus: 501, evidenceMessage: "not implemented" })
    render(<InterviewsTab runId="7" />)

    expect(
      await screen.findByText(/Основания ещё не связаны с ответами/),
    ).toBeInTheDocument()
    expect(
      await screen.findByText(/Раздел пока не поддерживается сервером/),
    ).toBeInTheDocument()
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
    // История интервью при этом остаётся доступной.
    expect(
      screen.getByText("Каждую неделю я собираю требования у заказчика."),
    ).toBeInTheDocument()
  })

  it("на 500 показывает ошибку рядом с нейтральным состоянием и умеет повторить", async () => {
    stubInterviewsApi({ failFirstEvidence: true })
    render(<InterviewsTab runId="7" />)

    expect(await screen.findByRole("alert")).toHaveTextContent("основания недоступны")
    expect(screen.getByText(/Основания ещё не связаны с ответами/)).toBeInTheDocument()

    await userEvent.click(
      screen.getByRole("button", { name: "Повторить загрузку оснований" }),
    )

    expect(await screen.findByText("Собирать требования")).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument())
  })
})
