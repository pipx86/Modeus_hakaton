/**
 * Импорт образовательной программы: загрузка файла с кодом программы, карточка
 * черновика, предупреждения, публикация с отчётом, отклонение, список черновиков,
 * а также состояния 403, 501 и пустого списка. Сеть подменена общим fetch-stub.
 */
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { ProgramImportTab } from "@/components/program-import-tab"

const IMPORT_RESPONSE = {
  draft_id: "draft-1",
  program: {
    title: "Прикладная математика и информатика",
    code: "01.03.02",
    organization: "Институт математики",
    competencies: [
      {
        code: "ОПК-1",
        type: "general_professional",
        title: "Способен применять фундаментальные знания",
        taxonomy_codes: ["tax-1"],
        ksa: [
          {
            kind: "knowledge",
            title: "Основы математического анализа",
            taxonomy_codes: ["tax-1"],
          },
          {
            kind: "skill",
            title: "Решать прикладные задачи",
            taxonomy_codes: [],
          },
        ],
      },
      {
        code: "ПК-2",
        type: "professional",
        title: "Способен разрабатывать программное обеспечение",
        taxonomy_codes: [],
        ksa: [],
      },
    ],
  },
  report: {
    competencies: 2,
    ksa: 2,
    warnings: ["У компетенции ОПК-1 не указана организация-разработчик."],
  },
  status: "draft",
}

const PUBLISH_RESPONSE = {
  program_id: "program-7",
  unmatched_competencies: 1,
  unmatched_ksa: 2,
  unknown_taxonomy_codes: ["tax-9", "tax-10"],
}

function selectFile(name = "program.csv"): File {
  const file = new File(["code,title\nОПК-1,Компетенция"], name, { type: "text/csv" })
  fireEvent.change(screen.getByLabelText("Файл программы"), { target: { files: [file] } })
  return file
}

function fillProgramKey(value = "01.03.02") {
  fireEvent.change(screen.getByLabelText("Код программы"), { target: { value } })
}

/** Обработчик основного сценария: пустой список, импорт, публикация. */
function stubImportFlow() {
  let published = false
  setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.endsWith("/api/v1/admin/programs/drafts") && method === "GET") {
      return {
        status: 200,
        body: {
          items: published
            ? [
                {
                  id: "draft-1",
                  program_key: "01.03.02",
                  source_name: "program.csv",
                  status: "published",
                  created_at: "2026-11-21T10:00:00Z",
                  competencies: 2,
                  ksa: 2,
                },
              ]
            : [],
        },
      }
    }
    if (url.endsWith("/api/v1/admin/programs/import") && method === "POST") {
      return { status: 201, body: IMPORT_RESPONSE }
    }
    if (
      url.endsWith("/api/v1/admin/programs/drafts/draft-1/publish") &&
      method === "POST"
    ) {
      published = true
      return { status: 200, body: PUBLISH_RESPONSE }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

describe("ProgramImportTab", () => {
  it("загружает программу и публикует черновик с отчётом", async () => {
    stubImportFlow()
    render(<ProgramImportTab />)

    expect(await screen.findByText("Черновиков пока нет.")).toBeInTheDocument()

    fillProgramKey()
    const file = selectFile()
    fireEvent.click(screen.getByRole("button", { name: "Загрузить" }))

    expect(
      await screen.findByText("Прикладная математика и информатика"),
    ).toBeInTheDocument()
    expect(screen.getByText("Способен применять фундаментальные знания")).toBeInTheDocument()
    expect(screen.getByText("Способен разрабатывать программное обеспечение")).toBeInTheDocument()
    expect(screen.getByText("Тип")).toBeInTheDocument()
    expect(screen.getByText("ОПК")).toBeInTheDocument()
    expect(screen.getByText("ПК")).toBeInTheDocument()
    expect(screen.getByText("Компетенций: 2 · KSA: 2")).toBeInTheDocument()
    expect(
      screen.getByText("У компетенции ОПК-1 не указана организация-разработчик."),
    ).toBeInTheDocument()

    // Технические id не показываются.
    expect(screen.queryByText(/draft-1/)).not.toBeInTheDocument()
    expect(screen.queryByText(/program-7/)).not.toBeInTheDocument()

    const uploadCall = fetchCalls().find((call) =>
      call.url.endsWith("/api/v1/admin/programs/import"),
    )
    expect(uploadCall?.init.method).toBe("POST")
    const form = uploadCall?.init.body as FormData
    expect(form.get("key")).toBe("01.03.02")
    expect(form.get("file")).toBe(file)

    // KSA раскрываются по компетенции.
    expect(screen.queryByText("Основы математического анализа")).not.toBeInTheDocument()
    fireEvent.click(
      screen.getByRole("button", {
        name: "Показать KSA: Способен применять фундаментальные знания",
      }),
    )
    expect(screen.getByText("Основы математического анализа")).toBeInTheDocument()
    expect(screen.getByText("Знание")).toBeInTheDocument()
    expect(screen.getByText("Умение")).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText("Таксономия"), {
      target: { value: "ФГОС ВО" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Опубликовать" }))

    expect(await screen.findByText("Не сопоставлено компетенций: 1")).toBeInTheDocument()
    expect(screen.getByText("Не сопоставлено KSA: 2")).toBeInTheDocument()
    expect(
      screen.getByText("Неизвестные коды таксономии: tax-9, tax-10"),
    ).toBeInTheDocument()
    expect(screen.getByText("Черновик уже опубликован.")).toBeInTheDocument()

    const publishCall = fetchCalls().find((call) =>
      call.url.endsWith("/api/v1/admin/programs/drafts/draft-1/publish"),
    )
    expect(publishCall?.init.method).toBe("POST")
    expect(JSON.parse(String(publishCall?.init.body))).toEqual({ taxonomy: "ФГОС ВО" })

    await waitFor(() =>
      expect(fetchCalls().some((call) => call.url.endsWith("/drafts/draft-1/publish"))).toBe(true),
    )
  })

  it("на 422 показывает сообщение разбора и предупреждения", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/admin/programs/drafts") && method === "GET") {
        return { status: 200, body: { items: [] } }
      }
      if (url.endsWith("/api/v1/admin/programs/import") && method === "POST") {
        return {
          status: 422,
          body: {
            code: "PROGRAM_PARSE_FAILED",
            message: "Не удалось разобрать файл программы",
            warnings: [
              "Строка 3: у компетенции нет формулировки.",
              "Файл содержит только заголовок таблицы.",
            ],
          },
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramImportTab />)

    expect(await screen.findByText("Черновиков пока нет.")).toBeInTheDocument()
    fillProgramKey()
    selectFile()
    fireEvent.click(screen.getByRole("button", { name: "Загрузить" }))

    const alert = await screen.findByRole("alert")
    expect(
      within(alert).getByText("Не удалось разобрать файл программы"),
    ).toBeInTheDocument()
    expect(
      within(alert).getByText("Строка 3: у компетенции нет формулировки."),
    ).toBeInTheDocument()
    expect(
      within(alert).getByText("Файл содержит только заголовок таблицы."),
    ).toBeInTheDocument()
    expect(screen.getByText("Предупреждения разбора (2)")).toBeInTheDocument()
    expect(
      screen.queryByRole("region", { name: "Черновик программы" }),
    ).not.toBeInTheDocument()
  })

  it("на 409 при публикации показывает причину конфликта", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/admin/programs/drafts") && method === "GET") {
        return { status: 200, body: { items: [] } }
      }
      if (url.endsWith("/api/v1/admin/programs/import") && method === "POST") {
        return { status: 201, body: IMPORT_RESPONSE }
      }
      if (
        url.endsWith("/api/v1/admin/programs/drafts/draft-1/publish") &&
        method === "POST"
      ) {
        return {
          status: 409,
          body: { code: "DRAFT_ALREADY_PUBLISHED", message: "Черновик уже опубликован" },
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramImportTab />)

    expect(await screen.findByText("Черновиков пока нет.")).toBeInTheDocument()
    fillProgramKey()
    selectFile()
    fireEvent.click(screen.getByRole("button", { name: "Загрузить" }))
    expect(
      await screen.findByText("Прикладная математика и информатика"),
    ).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Опубликовать" }))

    const alert = await screen.findByRole("alert")
    expect(within(alert).getByText("Черновик уже опубликован")).toBeInTheDocument()
    expect(screen.queryByText("Не сопоставлено компетенций: 1")).not.toBeInTheDocument()
    // Карточка остаётся в статусе черновика, решение можно повторить.
    expect(screen.getByRole("button", { name: "Опубликовать" })).toBeInTheDocument()
  })

  it("на 403 показывает, что раздел доступен администратору", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/admin/programs/drafts") && method === "GET") {
        return { status: 403, body: { code: "FORBIDDEN", message: "admin only" } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramImportTab />)

    expect(await screen.findByText("Раздел доступен администратору")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Загрузить" })).not.toBeInTheDocument()
  })

  it("на 501 показывает, что сервер не поддерживает импорт", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/admin/programs/drafts") && method === "GET") {
        return { status: 501, body: { code: "NOT_IMPLEMENTED", message: "not implemented" } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramImportTab />)

    expect(await screen.findByText("Сервер не поддерживает импорт")).toBeInTheDocument()
  })

  it("показывает пустой список и блокирует загрузку без файла и кода", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/admin/programs/drafts") && method === "GET") {
        return { status: 200, body: { items: [] } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramImportTab />)

    expect(await screen.findByText("Черновиков пока нет.")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Загрузить" })).toBeDisabled()

    fillProgramKey("02.04.01")
    selectFile("plan.xlsx")
    expect(screen.getByRole("button", { name: "Загрузить" })).toBeEnabled()
  })

  it("показывает тип компетенции отдельной колонкой", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/admin/programs/drafts") && method === "GET") {
        return { status: 200, body: { items: [] } }
      }
      if (url.endsWith("/api/v1/admin/programs/import") && method === "POST") {
        return {
          status: 201,
          body: {
            draft_id: "draft-3",
            program: {
              title: "Программа с типами компетенций",
              code: "03.03.03",
              organization: "Институт",
              competencies: [
                { code: "Б-1", type: "universal", title: "Базовые знания", ksa: [] },
                {
                  code: "ОПК-2",
                  type: "general_professional",
                  title: "Общие профессиональные",
                  ksa: [],
                },
                { code: "ПК-3", type: "professional", title: "Профессиональные", ksa: [] },
                { code: "И-4", type: "other", title: "Иные", ksa: [] },
                { code: "Б-5", title: "Без типа", ksa: [] },
              ],
            },
            report: {
              competencies: 5,
              ksa: 0,
              warnings: ["У компетенции Б-5 не указан тип."],
            },
            status: "draft",
          },
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramImportTab />)

    expect(await screen.findByText("Черновиков пока нет.")).toBeInTheDocument()
    fillProgramKey("03.03.03")
    selectFile("types.json")
    fireEvent.click(screen.getByRole("button", { name: "Загрузить" }))
    expect(
      await screen.findByText("Программа с типами компетенций"),
    ).toBeInTheDocument()

    const row = (title: string) => {
      const rowElement = screen.getByText(title).closest("tr")
      expect(rowElement).not.toBeNull()
      return rowElement as HTMLElement
    }
    expect(screen.getByText("Тип")).toBeInTheDocument()
    expect(within(row("Базовые знания")).getByText("УК")).toBeInTheDocument()
    expect(within(row("Общие профессиональные")).getByText("ОПК")).toBeInTheDocument()
    expect(within(row("Профессиональные")).getByText("ПК")).toBeInTheDocument()
    expect(within(row("Иные")).getByText("иное")).toBeInTheDocument()
    expect(within(row("Без типа")).getByText("-")).toBeInTheDocument()
    expect(screen.getByText("У компетенции Б-5 не указан тип.")).toBeInTheDocument()
  })

  it("открывает черновик из списка и отклоняет его", async () => {
    let rejected = false
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/admin/programs/drafts") && method === "GET") {
        return {
          status: 200,
          body: {
            items: [
              {
                id: "draft-2",
                program_key: "02.04.01",
                source_name: "plan.xlsx",
                status: rejected ? "rejected" : "draft",
                created_at: "2026-11-20T09:00:00Z",
                competencies: 1,
                ksa: 0,
              },
            ],
          },
        }
      }
      if (url.endsWith("/api/v1/admin/programs/drafts/draft-2/reject") && method === "POST") {
        rejected = true
        return { status: 204, body: undefined }
      }
      if (url.endsWith("/api/v1/admin/programs/drafts/draft-2") && method === "GET") {
        return {
          status: 200,
          body: {
            program_key: "02.04.01",
            source_name: "plan.xlsx",
            status: "draft",
            program: {
              title: "Программная инженерия",
              code: "02.04.01",
              organization: "Институт информатики",
              competencies: [
                { code: "ПК-1", title: "Способен проектировать системы", ksa: [] },
              ],
            },
            report: { competencies: 1, ksa: 0, warnings: [] },
          },
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    render(<ProgramImportTab />)

    expect(await screen.findByText("02.04.01")).toBeInTheDocument()
    expect(screen.getByText("Черновик")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Открыть" }))

    expect(await screen.findByText("Программная инженерия")).toBeInTheDocument()
    expect(screen.getByText("Код программы: 02.04.01")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Отклонить" }))

    expect(await screen.findByText("Черновик отклонён.")).toBeInTheDocument()
    await waitFor(() =>
      expect(
        fetchCalls().some((call) =>
          call.url.endsWith("/api/v1/admin/programs/drafts/draft-2/reject"),
        ),
      ).toBe(true),
    )
  })
})
