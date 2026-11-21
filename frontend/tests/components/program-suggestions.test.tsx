/**
 * Предложения по программе: приоритеты, виды предложений, rationale,
 * фиксированный disclaimer и нейтральные пустые состояния. 501 - спокойное
 * «раздел пока не поддерживается», 500 - сообщение с повтором. Сеть подменена
 * общим fetch-stub.
 */
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { setFetchHandler } from "@/test/fetch-stub"
import { ProgramSuggestions } from "@/components/program-suggestions"
import type { ProgramSuggestions as ProgramSuggestionsResponse } from "@/lib/api/client"

const DISCLAIMER =
  "предложения системы, решение принимает методист; система не утверждает и не переписывает программу"

const SUGGESTIONS: ProgramSuggestionsResponse = {
  run_id: "7",
  program_id: "p-1",
  program_title: "Прикладная математика",
  generated_at: "2026-11-21T10:00:00Z",
  proposals: [
    {
      id: "unmapped_activity:n-1",
      kind: "unmapped_activity",
      target: { node_id: "n-1" },
      rationale: "У задачи «Собирать требования» нет ни одной связи с компетенциями программы; кандидат — рассмотреть добавление в программу.",
      priority: "high",
      status: "proposal",
    },
    {
      id: "unconfirmed_competency:c-2",
      kind: "unconfirmed_competency",
      target: { competency_id: "c-2", competency_title: "Коммуникация" },
      rationale: "У компетенции «Коммуникация» есть предложенные связи (1), но ни одна не подтверждена; стоит проверить и подтвердить или отклонить.",
      priority: "medium",
      status: "proposal",
    },
    {
      id: "uncovered_competency:c-3",
      kind: "uncovered_competency",
      target: { competency_id: "c-3", competency_title: "Проектирование" },
      rationale: "Компетенция «Проектирование» не связана ни с одним элементом деятельности этого исследования; стоит проверить, насколько она актуальна для программы.",
      priority: "low",
      status: "proposal",
    },
    {
      id: "conflicting_links:n-2",
      kind: "conflicting_links",
      target: { node_id: "n-2" },
      rationale: "У элемента «Согласовывать макет» есть подтверждённые связи (Анализ данных) и отклонённые связи (Дизайн) с разными компетенциями; стоит пересмотреть, какая связь верна.",
      priority: "low",
      status: "proposal",
    },
  ],
  summary: "Предложений: 4. Высокий приоритет: 1, средний: 1, низкий: 2. Решение принимает методист.",
  disclaimer: DISCLAIMER,
}

interface StubOptions {
  suggestions?: ProgramSuggestionsResponse
  status?: number
  message?: string
}

/** Обработчик предложений по программе. */
function stubSuggestionsApi(options: StubOptions = {}) {
  setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.endsWith("/api/v1/runs/7/program-suggestions") && method === "GET") {
      const status = options.status ?? 200
      if (status >= 400) {
        return { status, body: { code: "INTERNAL", message: options.message ?? "сломалось" } }
      }
      return { status: 200, body: options.suggestions ?? SUGGESTIONS }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

describe("ProgramSuggestions", () => {
  it("показывает предложения, приоритеты, цели и disclaimer", async () => {
    stubSuggestionsApi()
    render(<ProgramSuggestions runId="7" />)

    expect(await screen.findByText("Предложения по программе")).toBeInTheDocument()
    expect(screen.getByText("Прикладная математика")).toBeInTheDocument()
    expect(screen.getByText("Высокий: 1")).toBeInTheDocument()
    expect(screen.getByText("Средний: 1")).toBeInTheDocument()
    expect(screen.getByText("Низкий: 2")).toBeInTheDocument()
    expect(screen.getByText("Всего: 4")).toBeInTheDocument()
    expect(screen.getByText(SUGGESTIONS.summary ?? "")).toBeInTheDocument()

    // Все четыре вида предложений видны с русскими подписями.
    expect(screen.getByText("Элемент деятельности без подтверждённой связи")).toBeInTheDocument()
    expect(screen.getByText("Неподтверждённая компетенция")).toBeInTheDocument()
    expect(screen.getByText("Компетенция без связей с деятельностью")).toBeInTheDocument()
    expect(screen.getByText("Противоречивые связи")).toBeInTheDocument()
    expect(screen.getAllByText("Предложение")).toHaveLength(4)
    expect(screen.getByText("Высокий приоритет")).toBeInTheDocument()
    expect(screen.getByText("Коммуникация")).toBeInTheDocument()

    // Оговорка карточки 32 обязательна и показывается полностью.
    expect(screen.getByText(DISCLAIMER)).toBeInTheDocument()
    // Формулировки нейтральные: нет «пробела» и «недостатка программы».
    expect(screen.queryByText(/пробел/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/недостат/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/слаб/i)).not.toBeInTheDocument()
  })

  it("показывает пустой список с объяснением и disclaimer", async () => {
    stubSuggestionsApi({
      suggestions: {
        ...SUGGESTIONS,
        proposals: [],
        summary: "Связей элементов деятельности с компетенциями программы пока нет — предложений нет.",
      },
    })
    render(<ProgramSuggestions runId="7" />)

    expect(await screen.findByText("Предложений нет")).toBeInTheDocument()
    expect(
      screen.getByText(
        "Связей элементов деятельности с компетенциями программы пока нет — предложений нет.",
      ),
    ).toBeInTheDocument()
    expect(screen.getByText(DISCLAIMER)).toBeInTheDocument()
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
  })

  it("на 501 показывает нейтральное пустое состояние", async () => {
    stubSuggestionsApi({ status: 501, message: "not implemented" })
    render(<ProgramSuggestions runId="7" />)

    expect(await screen.findByText("Предложения пока недоступны")).toBeInTheDocument()
    expect(screen.getByText("Раздел пока не поддерживается сервером.")).toBeInTheDocument()
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
  })

  it("на 500 показывает видимую ошибку и повторяет загрузку", async () => {
    let calls = 0
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/runs/7/program-suggestions") && method === "GET") {
        calls += 1
        return calls === 1
          ? { status: 500, body: { code: "INTERNAL", message: "предложения недоступны" } }
          : { status: 200, body: SUGGESTIONS }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramSuggestions runId="7" />)

    expect(await screen.findByText("предложения недоступны")).toBeInTheDocument()
    await userEvent.click(
      screen.getByRole("button", { name: "Повторить загрузку предложений" }),
    )
    expect(await screen.findByText("Прикладная математика")).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument())
  })

  it("не падает, если вместо объекта пришёл массив", async () => {
    setFetchHandler(async (url, init) => {
      if (url.endsWith("/api/v1/runs/7/program-suggestions") && (init?.method ?? "GET") === "GET") {
        return { status: 200, body: [] }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    const { container } = render(<ProgramSuggestions runId="7" />)

    expect(await screen.findByText("Предложений нет")).toBeInTheDocument()
    expect(screen.queryByRole("list", { name: "Предложения по программе" })).not.toBeInTheDocument()
    expect(container.textContent).not.toMatch(/пробел|недостат/i)
  })
})
