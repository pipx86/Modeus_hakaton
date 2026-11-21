/**
 * Вкладка «Программа»: группировка связей по статусу, решение методиста,
 * пустые состояния (включая 501), фильтр по узлу и readonly-режим.
 * Тесты используют общий fetch-stub проекта.
 */
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { ProgramLinksTab } from "@/components/program-links-tab"
import type { ProgramLink } from "@/lib/api/client"

const LINKS: ProgramLink[] = [
  {
    id: "l-1",
    run_id: "7",
    node_id: "n-1",
    node_title: "Анализ метрик",
    node_type: "function",
    program_id: "p-1",
    program_title: "Прикладная математика",
    competency_id: "c-1",
    competency_title: "Анализ данных",
    ksa_id: "k-1",
    ksa_title: "Строить отчёты",
    status: "proposed",
    reason: "Совпадают формулировки о метриках",
    source: "system",
    reviewed_at: null,
  },
  {
    id: "l-2",
    run_id: "7",
    node_id: "n-2",
    node_title: "Подтверждённая связь",
    node_type: "task",
    program_id: "p-1",
    program_title: "Прикладная математика",
    competency_id: "c-2",
    competency_title: "Статистика",
    ksa_id: null,
    ksa_title: null,
    status: "approved",
    reason: "Связь согласована методистом",
    source: "methodist",
    reviewed_at: "2026-11-19T10:00:00Z",
  },
  {
    id: "l-3",
    run_id: "7",
    node_id: "n-3",
    node_title: "Отклонённая связь",
    node_type: "action",
    program_id: "p-1",
    program_title: "Прикладная математика",
    competency_id: "c-3",
    competency_title: "Коммуникации",
    ksa_id: null,
    ksa_title: null,
    status: "rejected",
    reason: "Не относится к программе",
    source: "methodist",
    reviewed_at: "2026-11-19T11:00:00Z",
  },
]

interface StubOptions {
  links?: ProgramLink[]
  status?: number
  message?: string
  reviewStatus?: number
  reviewMessage?: string
}

/** Обработчик списка и решения по связям: после решения список обновляется. */
function stubLinks(options: StubOptions = {}) {
  let current = (options.links ?? LINKS).map((link) => ({ ...link }))
  setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.includes("/program-links/") && url.endsWith("/review") && method === "POST") {
      if (options.reviewStatus && options.reviewStatus >= 400) {
        return {
          status: options.reviewStatus,
          body: { code: "INTERNAL", message: options.reviewMessage ?? "не удалось сохранить" },
        }
      }
      const body = JSON.parse(String(init.body)) as { status: "approved" | "rejected"; reason: string }
      const linkId = url.split("/program-links/")[1]?.replace("/review", "")
      current = current.map((link) =>
        link.id === linkId
          ? {
              ...link,
              status: body.status,
              reason: body.reason || link.reason,
              source: "methodist" as const,
              reviewed_at: "2026-11-20T10:00:00Z",
            }
          : link,
      )
      return { status: 200, body: current.find((link) => link.id === linkId) }
    }
    if (url.includes("/program-links")) {
      if (options.status && options.status >= 400) {
        return {
          status: options.status,
          body: { code: "INTERNAL", message: options.message ?? "сломалось" },
        }
      }
      return { status: 200, body: current }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

describe("ProgramLinksTab", () => {
  it("группирует связи: не проверено и подтверждено видны, отклонённые свёрнуты", async () => {
    stubLinks()
    render(<ProgramLinksTab runId="7" />)

    expect(await screen.findByText("Анализ метрик")).toBeInTheDocument()
    expect(screen.getByText("Анализ данных")).toBeInTheDocument()
    expect(screen.getByText("Строить отчёты")).toBeInTheDocument()
    expect(screen.getByText("Совпадают формулировки о метриках")).toBeInTheDocument()
    expect(screen.getByText("Подтверждённая связь")).toBeInTheDocument()

    const proposedGroup = screen.getByRole("button", { name: /^Не проверено/ })
    expect(proposedGroup).toHaveAttribute("aria-expanded", "true")
    expect(screen.queryByText("Отклонённая связь")).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: /^Отклонённые/ }))

    expect(await screen.findByText("Отклонённая связь")).toBeInTheDocument()
    expect(screen.getByText("Не относится к программе")).toBeInTheDocument()
  })

  it("подтверждает связь с комментарием и перезагружает список", async () => {
    stubLinks()
    render(<ProgramLinksTab runId="7" />)
    await screen.findByText("Анализ метрик")

    fireEvent.change(screen.getAllByLabelText("Комментарий к решению (необязательно)")[0], {
      target: { value: "Подходит по компетенции" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Подтвердить" }))

    await waitFor(() => {
      const post = fetchCalls().find(
        (call) => call.url.includes("/program-links/l-1/review") && call.init.method === "POST",
      )
      expect(post).toBeDefined()
      expect(JSON.parse(String(post!.init.body))).toEqual({
        status: "approved",
        reason: "Подходит по компетенции",
      })
    })

    await waitFor(() => {
      const approvedGroup = screen
        .getByRole("button", { name: /^Подтверждено/ })
        .closest("section") as HTMLElement | null
      expect(approvedGroup).not.toBeNull()
      expect(within(approvedGroup as HTMLElement).getByText("Анализ метрик")).toBeInTheDocument()
    })
    expect(screen.queryByRole("button", { name: /^Не проверено/ })).not.toBeInTheDocument()
  })

  it("при ошибке решения оставляет связь в списке и показывает сообщение", async () => {
    stubLinks({ reviewStatus: 500, reviewMessage: "сервер не принял решение" })
    render(<ProgramLinksTab runId="7" />)
    await screen.findByText("Анализ метрик")

    fireEvent.click(screen.getByRole("button", { name: "Подтвердить" }))

    expect(await screen.findByRole("alert")).toHaveTextContent("сервер не принял решение")
    expect(screen.getByText("Анализ метрик")).toBeInTheDocument()
  })

  it("пустой список показывает нейтральное состояние, счётчик 0 и обновление", async () => {
    stubLinks({ links: [] })
    render(<ProgramLinksTab runId="7" />)

    expect(await screen.findByText("Сопоставления ещё не сформированы")).toBeInTheDocument()
    // Счётчик нуля не дублирует пустое состояние: его нет.
    expect(screen.queryByText(/Сопоставлений:/)).not.toBeInTheDocument()
    expect(screen.getByText(/Предложения по связям узлов деятельности/)).toBeInTheDocument()
    expect(screen.queryByText(/пробел в программе/i)).not.toBeInTheDocument()

    const requestsBefore = fetchCalls().filter((call) => call.url.includes("/program-links")).length
    fireEvent.click(screen.getByRole("button", { name: "Обновить" }))

    await waitFor(() =>
      expect(
        fetchCalls().filter((call) => call.url.includes("/program-links")).length,
      ).toBeGreaterThan(requestsBefore),
    )
  })

  it("на 501 показывает то же пустое состояние с пометкой, а не ошибку", async () => {
    stubLinks({ status: 501, message: "not implemented" })
    render(<ProgramLinksTab runId="7" />)

    expect(await screen.findByText("Сопоставления ещё не сформированы")).toBeInTheDocument()
    expect(screen.getByText(/Сопоставление пока не включено/)).toBeInTheDocument()
    expect(screen.queryByText(/Не удалось/)).not.toBeInTheDocument()
  })

  it("на 500 показывает ошибку с кнопкой повтора, а не пустой список", async () => {
    stubLinks({ status: 500, message: "ядро недоступно" })
    render(<ProgramLinksTab runId="7" />)

    expect(await screen.findByText("ядро недоступно")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Повторить" })).toBeInTheDocument()
    expect(screen.queryByText("Сопоставления ещё не сформированы")).not.toBeInTheDocument()
  })

  it("фильтрует связи по узлу и сбрасывает фильтр", async () => {
    const onClear = vi.fn()
    stubLinks({
      links: [
        LINKS[0],
        { ...LINKS[1], node_id: "n-2", node_title: "Другая связь" },
      ],
    })
    render(<ProgramLinksTab runId="7" nodeId="n-1" onClearNodeFilter={onClear} />)

    // Название узла есть и в строке связи, и в баннере фильтра.
    expect((await screen.findAllByText("Анализ метрик")).length).toBeGreaterThan(0)
    expect(screen.queryByText("Другая связь")).not.toBeInTheDocument()
    expect(screen.getByText(/Показаны сопоставления узла/)).toBeInTheDocument()
    expect(
      fetchCalls().some((call) => call.url.includes("/program-links?node_id=n-1")),
    ).toBe(true)

    fireEvent.click(screen.getByRole("button", { name: "Показать все" }))
    expect(onClear).toHaveBeenCalled()
  })

  it("в readonly не показывает решение и поле комментария", async () => {
    stubLinks()
    render(<ProgramLinksTab runId="7" readOnly />)

    expect(await screen.findByText("Анализ метрик")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Подтвердить" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Отклонить" })).not.toBeInTheDocument()
    expect(
      screen.queryByLabelText("Комментарий к решению (необязательно)"),
    ).not.toBeInTheDocument()
  })
})
