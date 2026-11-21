/**
 * Реестр источников программ: загрузка списка, добавление, сообщения 400 и 409,
 * ручная проверка (unchanged, changed с черновиком, needs_parser, error),
 * переключение enabled, удаление с подтверждением, 403, 501 и пустое состояние.
 * Сеть подменена общим fetch-stub, как в тестах импорта программы.
 */
import { fireEvent, render, screen, within } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { ProgramWatchTab } from "@/components/program-watch-tab"

const PROGRAM_KEY = "mgu/01.03.02/2026/full-time"
const PROGRAM_URL = "https://mgu.example.ru/programs/01.03.02"

const SOURCE = {
  id: "source-1",
  program_key: PROGRAM_KEY,
  url: PROGRAM_URL,
  doc_format: "html",
  interval_hours: 24,
  enabled: true,
  last_checked_at: "2026-11-21T09:00:00Z",
  last_status: "unchanged",
  last_error: null,
  draft_id: null,
}

const SOURCES_ENDPOINT = "/api/v1/admin/program-sources"

/** Обработчик списка: остальные маршруты отвечают 404, чтобы тест не терял запросы. */
function stubList(items: unknown[]) {
  setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.endsWith(SOURCES_ENDPOINT) && method === "GET") {
      return { status: 200, body: { items } }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

function fillAddForm(key = PROGRAM_KEY, url = PROGRAM_URL) {
  fireEvent.change(screen.getByLabelText("Ключ программы"), { target: { value: key } })
  fireEvent.change(screen.getByLabelText("Ссылка на программу"), { target: { value: url } })
}

/** Строка списка по ключу программы: действия ищем внутри неё. */
function sourceRow(key = PROGRAM_KEY): HTMLElement {
  const row = screen.getByText(key).closest("li")
  expect(row).not.toBeNull()
  return row as HTMLElement
}

describe("ProgramWatchTab", () => {
  it("загружает список источников и не показывает технические id", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith(SOURCES_ENDPOINT) && method === "GET") {
        return {
          status: 200,
          body: {
            items: [
              SOURCE,
              {
                ...SOURCE,
                id: "source-2",
                program_key: "spbu/02.04.01/2026/part-time",
                url: "https://spbu.ru/programs/02.04.01",
                interval_hours: 48,
                last_status: "error",
                last_error: "Сайт вернул 404",
                draft_id: "draft-2",
              },
            ],
          },
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramWatchTab onOpenDraft={() => undefined} />)

    expect(await screen.findByText(PROGRAM_KEY)).toBeInTheDocument()
    expect(screen.getByText("spbu/02.04.01/2026/part-time")).toBeInTheDocument()
    expect(screen.getAllByText("mgu.example.ru").length).toBeGreaterThan(0)
    expect(screen.getByText(/каждые 24 ч/)).toBeInTheDocument()
    expect(screen.getByText("Без изменений")).toBeInTheDocument()
    expect(screen.getByText("Ошибка")).toBeInTheDocument()
    expect(screen.getByText("Черновик")).toBeInTheDocument()

    // Дата последней проверки приходит в машиночитаемом виде и не теряется.
    expect(document.querySelector('time[datetime="2026-11-21T09:00:00Z"]')).not.toBeNull()

    // Технические id источника и черновика на экране не показываются.
    expect(screen.queryByText(/source-1/)).not.toBeInTheDocument()
    expect(screen.queryByText(/source-2/)).not.toBeInTheDocument()
    expect(screen.queryByText(/draft-2/)).not.toBeInTheDocument()
  })

  it("добавляет источник и отправляет интервал и формат", async () => {
    let items: unknown[] = []
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith(SOURCES_ENDPOINT) && method === "GET") {
        return { status: 200, body: { items } }
      }
      if (url.endsWith(SOURCES_ENDPOINT) && method === "POST") {
        const created = { ...SOURCE, id: "source-3", last_checked_at: null, last_status: null }
        items = [created]
        return { status: 201, body: created }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramWatchTab />)

    expect(await screen.findByText("Источников пока нет.")).toBeInTheDocument()
    expect(screen.getByLabelText("Интервал проверки, часы")).toHaveValue(24)

    fillAddForm()
    fireEvent.change(screen.getByLabelText("Формат документа (необязательно)"), {
      target: { value: "html" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Добавить источник" }))

    expect(await screen.findByText(PROGRAM_KEY)).toBeInTheDocument()
    const createCall = fetchCalls().find(
      (call) => call.url.endsWith(SOURCES_ENDPOINT) && call.init.method === "POST",
    )
    expect(createCall).toBeDefined()
    expect(JSON.parse(String(createCall?.init.body))).toEqual({
      program_key: PROGRAM_KEY,
      url: PROGRAM_URL,
      interval_hours: 24,
      doc_format: "html",
    })
  })

  it("на 400 объясняет формат ключа", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith(SOURCES_ENDPOINT) && method === "GET") {
        return { status: 200, body: { items: [] } }
      }
      if (url.endsWith(SOURCES_ENDPOINT) && method === "POST") {
        return {
          status: 400,
          body: { code: "INVALID_PROGRAM_KEY", message: "program_key must match org/code/year/form" },
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramWatchTab />)

    expect(await screen.findByText("Источников пока нет.")).toBeInTheDocument()
    fillAddForm("mgu/01.03.02")
    fireEvent.click(screen.getByRole("button", { name: "Добавить источник" }))

    expect(await screen.findByText(`Ключ должен быть вида ${PROGRAM_KEY}`)).toBeInTheDocument()
  })

  it("на 409 сообщает, что такой источник уже есть", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith(SOURCES_ENDPOINT) && method === "GET") {
        return { status: 200, body: { items: [] } }
      }
      if (url.endsWith(SOURCES_ENDPOINT) && method === "POST") {
        return {
          status: 409,
          body: { code: "PROGRAM_SOURCE_EXISTS", message: "такой источник уже есть" },
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramWatchTab />)

    expect(await screen.findByText("Источников пока нет.")).toBeInTheDocument()
    fillAddForm()
    fireEvent.click(screen.getByRole("button", { name: "Добавить источник" }))

    expect(await screen.findByText("Такой источник уже есть")).toBeInTheDocument()
  })

  it("после проверки показывает, что изменений нет", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith(SOURCES_ENDPOINT) && method === "GET") {
        return { status: 200, body: { items: [SOURCE] } }
      }
      if (url.endsWith(`${SOURCES_ENDPOINT}/source-1/check`) && method === "POST") {
        return { status: 200, body: { status: "unchanged" } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramWatchTab />)

    await screen.findByText(PROGRAM_KEY)
    fireEvent.click(within(sourceRow()).getByRole("button", { name: "Проверить сейчас" }))

    expect(await screen.findByText("Изменений нет")).toBeInTheDocument()
    expect(
      fetchCalls().some(
        (call) =>
          call.url.endsWith(`${SOURCES_ENDPOINT}/source-1/check`) &&
          call.init.method === "POST",
      ),
    ).toBe(true)
  })

  it("после проверки показывает созданный черновик и открывает его", async () => {
    const openDraft = vi.fn()
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith(SOURCES_ENDPOINT) && method === "GET") {
        return { status: 200, body: { items: [SOURCE] } }
      }
      if (url.endsWith(`${SOURCES_ENDPOINT}/source-1/check`) && method === "POST") {
        return { status: 200, body: { status: "changed", draft_id: "draft-9" } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramWatchTab onOpenDraft={openDraft} />)

    await screen.findByText(PROGRAM_KEY)
    fireEvent.click(within(sourceRow()).getByRole("button", { name: "Проверить сейчас" }))

    expect(await screen.findByText("Создан черновик")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Открыть" }))
    expect(openDraft).toHaveBeenCalledWith("draft-9")
  })

  it("после проверки объясняет, что PDF и DOCX пока не поддерживаются", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith(SOURCES_ENDPOINT) && method === "GET") {
        return { status: 200, body: { items: [SOURCE] } }
      }
      if (url.endsWith(`${SOURCES_ENDPOINT}/source-1/check`) && method === "POST") {
        return { status: 200, body: { status: "needs_parser" } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramWatchTab />)

    await screen.findByText(PROGRAM_KEY)
    fireEvent.click(within(sourceRow()).getByRole("button", { name: "Проверить сейчас" }))

    expect(
      await screen.findByText("PDF/DOCX пока не поддерживаются"),
    ).toBeInTheDocument()
  })

  it("после проверки показывает текст ошибки", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith(SOURCES_ENDPOINT) && method === "GET") {
        return { status: 200, body: { items: [SOURCE] } }
      }
      if (url.endsWith(`${SOURCES_ENDPOINT}/source-1/check`) && method === "POST") {
        return { status: 200, body: { status: "error", error: "Сайт вернул 404" } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramWatchTab />)

    await screen.findByText(PROGRAM_KEY)
    fireEvent.click(within(sourceRow()).getByRole("button", { name: "Проверить сейчас" }))

    expect(await screen.findByText("Сайт вернул 404")).toBeInTheDocument()
  })

  it("выключает источник через PATCH", async () => {
    let enabled = true
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith(SOURCES_ENDPOINT) && method === "GET") {
        return { status: 200, body: { items: [{ ...SOURCE, enabled }] } }
      }
      if (url.endsWith(`${SOURCES_ENDPOINT}/source-1`) && method === "PATCH") {
        enabled = false
        return { status: 200, body: { ...SOURCE, enabled: false } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramWatchTab />)

    await screen.findByText(PROGRAM_KEY)
    fireEvent.click(within(sourceRow()).getByRole("button", { name: "Выключить" }))

    expect(await within(sourceRow()).findByRole("button", { name: "Включить" })).toBeInTheDocument()
    expect(screen.getByText("Выключен")).toBeInTheDocument()
    const patchCall = fetchCalls().find(
      (call) =>
        call.url.endsWith(`${SOURCES_ENDPOINT}/source-1`) && call.init.method === "PATCH",
    )
    expect(JSON.parse(String(patchCall?.init.body))).toEqual({ enabled: false })
  })

  it("удаляет источник только после подтверждения", async () => {
    let items: unknown[] = [SOURCE]
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith(SOURCES_ENDPOINT) && method === "GET") {
        return { status: 200, body: { items } }
      }
      if (url.endsWith(`${SOURCES_ENDPOINT}/source-1`) && method === "DELETE") {
        items = []
        return { status: 204, body: undefined }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramWatchTab />)

    await screen.findByText(PROGRAM_KEY)
    fireEvent.click(within(sourceRow()).getByRole("button", { name: "Удалить" }))
    expect(screen.getByText("Удалить источник?")).toBeInTheDocument()
    expect(fetchCalls().some((call) => call.init.method === "DELETE")).toBe(false)

    fireEvent.click(screen.getByRole("button", { name: "Удалить" }))

    expect(await screen.findByText("Источников пока нет.")).toBeInTheDocument()
    expect(
      fetchCalls().some(
        (call) =>
          call.url.endsWith(`${SOURCES_ENDPOINT}/source-1`) &&
          call.init.method === "DELETE",
      ),
    ).toBe(true)
  })

  it("на 403 показывает, что раздел доступен администратору", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith(SOURCES_ENDPOINT) && method === "GET") {
        return { status: 403, body: { code: "FORBIDDEN", message: "admin only" } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramWatchTab />)

    expect(await screen.findByText("Раздел доступен администратору")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Добавить источник" })).not.toBeInTheDocument()
  })

  it("на 501 показывает, что сервер не поддерживает реестр", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith(SOURCES_ENDPOINT) && method === "GET") {
        return { status: 501, body: { code: "NOT_IMPLEMENTED", message: "not implemented" } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramWatchTab />)

    expect(await screen.findByText("Сервер не поддерживает реестр")).toBeInTheDocument()
  })

  it("показывает пустое состояние и блокирует добавление без ключа и ссылки", async () => {
    stubList([])
    render(<ProgramWatchTab />)

    expect(await screen.findByText("Источников пока нет.")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Добавить источник" })).toBeDisabled()

    fillAddForm("mgu/01.03.02/2026/full-time", PROGRAM_URL)
    expect(screen.getByRole("button", { name: "Добавить источник" })).toBeEnabled()
  })
})
