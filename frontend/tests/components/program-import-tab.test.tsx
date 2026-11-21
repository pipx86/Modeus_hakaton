/**
 * Импорт образовательной программы: загрузка файла с кодом программы, карточка
 * черновика, предупреждения, публикация с отчётом, отклонение, список черновиков,
 * а также состояния 403, 501 и пустого списка. Сеть подменена общим fetch-stub.
 *
 * Отдельный блок - выгрузка DOCX черновика: кнопка в карточке, Bearer-токен в
 * запросе, временная ссылка на blob и тосты для 403/501.
 *
 * Ещё один блок - добавление KSA в существующий черновик
 * (`POST .../drafts/{id}/ksa`): скрытый file-input, multipart с Authorization,
 * обновление карточки через `getProgramDraft`, тосты со счётчиками и
 * предупреждением о неизвестных кодах, ошибки 422/503/501 и повторная загрузка
 * без дубликатов.
 */
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { toast } from "sonner"
import { fetchCalls, setFetchHandler, type StubResponse } from "@/test/fetch-stub"
import { clearToken, storeToken } from "@/lib/auth"
import { ProgramImportTab } from "@/components/program-import-tab"

const DOCX_MIME =
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

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

/** Ответ сервера на добавление KSA: один новый элемент, два уже были. */
const KSA_RESPONSE = {
  draft_id: "draft-1",
  added: 1,
  already_present: 2,
  unknown_codes: [],
  warnings: [],
}

/** Черновик после добавления KSA: у ОПК-1 появился третий элемент. */
const UPDATED_DRAFT_DETAIL = {
  program_key: "01.03.02",
  source_name: "program.csv",
  status: "draft",
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
          {
            kind: "knowledge",
            title: "Теория вероятностей",
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
    ksa: 3,
    warnings: [],
  },
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

/** JWT-подобный токен: фронтенд только декодирует payload и проверяет exp. */
function makeToken(payload: Record<string, unknown>): string {
  const encode = (value: unknown) =>
    window.btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
  return `${encode({ alg: "HS256", typ: "JWT" })}.${encode(payload)}.signature`
}

/** Сценарий выгрузки: пустой список, импорт черновика draft-1, ответ export.docx. */
function stubDraftExport(exportResponse: StubResponse) {
  setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.endsWith("/api/v1/admin/programs/drafts") && method === "GET") {
      return { status: 200, body: { items: [] } }
    }
    if (url.endsWith("/api/v1/admin/programs/import") && method === "POST") {
      return { status: 201, body: IMPORT_RESPONSE }
    }
    if (url.endsWith("/api/v1/admin/programs/drafts/draft-1/export.docx")) {
      return exportResponse
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

/** Загружает IMPORT_RESPONSE и дожидается карточки черновика. */
async function uploadDraft() {
  expect(await screen.findByText("Черновиков пока нет.")).toBeInTheDocument()
  fillProgramKey()
  selectFile()
  fireEvent.click(screen.getByRole("button", { name: "Загрузить" }))
  expect(await screen.findByText("Прикладная математика и информатика")).toBeInTheDocument()
}

/**
 * Сценарий добавления KSA: пустой список, импорт черновика draft-1, ответ
 * `/drafts/draft-1/ksa` (объект или фабрика для повторных вызовов) и
 * перечитывание карточки через `GET /drafts/draft-1`.
 */
function stubKsaUpload(
  ksaResponse: StubResponse | (() => StubResponse) = { status: 200, body: KSA_RESPONSE },
  detail: unknown = UPDATED_DRAFT_DETAIL,
) {
  setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.endsWith("/api/v1/admin/programs/drafts") && method === "GET") {
      return { status: 200, body: { items: [] } }
    }
    if (url.endsWith("/api/v1/admin/programs/import") && method === "POST") {
      return { status: 201, body: IMPORT_RESPONSE }
    }
    if (url.endsWith("/api/v1/admin/programs/drafts/draft-1/ksa") && method === "POST") {
      return typeof ksaResponse === "function" ? ksaResponse() : ksaResponse
    }
    if (url.endsWith("/api/v1/admin/programs/drafts/draft-1") && method === "GET") {
      return { status: 200, body: detail }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

/** Выбирает файл в скрытом input добавления KSA. */
function selectKsaFile(name = "ksa.docx"): File {
  const file = new File(["ksa-content"], name, { type: DOCX_MIME })
  fireEvent.change(screen.getByLabelText("Файл KSA"), { target: { files: [file] } })
  return file
}

/** Заглушки blob-ссылок: jsdom не реализует createObjectURL. */
function stubObjectUrls() {
  const create = vi.fn<(blob: Blob) => string>(() => "blob:program-draft-docx")
  const revoke = vi.fn()
  Object.assign(URL, { createObjectURL: create, revokeObjectURL: revoke })
  return { create, revoke }
}

describe("ProgramImportTab", () => {
  afterEach(() => {
    vi.restoreAllMocks()
    clearToken()
  })

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

  it("показывает кнопку «Скачать DOCX» в карточке черновика", async () => {
    stubDraftExport({
      status: 200,
      body: new Blob(["docx"], { type: DOCX_MIME }),
      headers: { "content-type": DOCX_MIME },
    })
    render(<ProgramImportTab />)
    await uploadDraft()

    const card = screen.getByRole("region", { name: "Черновик программы" })
    // Кнопка выгрузки стоит рядом с решениями по черновику.
    expect(within(card).getByRole("button", { name: "Опубликовать" })).toBeInTheDocument()
    expect(within(card).getByRole("button", { name: "Отклонить" })).toBeInTheDocument()
    expect(within(card).getByRole("button", { name: "Скачать DOCX" })).toBeEnabled()
  })

  it("скачивает DOCX с Authorization и создаёт временную ссылку на blob", async () => {
    const token = makeToken({ sub: "admin", role: "admin", exp: 4102444800 })
    storeToken(token)
    stubDraftExport({
      status: 200,
      body: new Blob(["docx-bytes"], { type: DOCX_MIME }),
      headers: {
        "content-type": DOCX_MIME,
        "content-disposition": 'attachment; filename="program-01.03.02.docx"',
      },
    })
    const urls = stubObjectUrls()
    const anchors: HTMLAnchorElement[] = []
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(function (this: HTMLAnchorElement) {
        anchors.push(this)
      })
    const success = vi.spyOn(toast, "success")

    render(<ProgramImportTab />)
    await uploadDraft()
    fireEvent.click(screen.getByRole("button", { name: "Скачать DOCX" }))

    await waitFor(() => expect(click).toHaveBeenCalledTimes(1))

    const exportCall = fetchCalls().find((call) =>
      call.url.endsWith("/api/v1/admin/programs/drafts/draft-1/export.docx"),
    )
    expect(exportCall).toBeDefined()
    expect(exportCall?.init.method ?? "GET").toBe("GET")
    expect(new Headers(exportCall?.init.headers).get("Authorization")).toBe(`Bearer ${token}`)

    expect(urls.create).toHaveBeenCalledTimes(1)
    expect(urls.create.mock.calls[0]?.[0]).toBeInstanceOf(Blob)
    expect(urls.revoke).toHaveBeenCalledWith("blob:program-draft-docx")
    expect(anchors[0]?.download).toBe("program-01.03.02.docx")
    expect(anchors[0]?.href).toBe("blob:program-draft-docx")
    await waitFor(() => expect(success).toHaveBeenCalledWith("Файл сохранён"))
  })

  it("на 403 показывает тост, что раздел доступен администратору, и не сохраняет файл", async () => {
    stubDraftExport({
      status: 403,
      body: { code: "FORBIDDEN", message: "admin only" },
    })
    const urls = stubObjectUrls()
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {})
    const error = vi.spyOn(toast, "error")

    render(<ProgramImportTab />)
    await uploadDraft()
    fireEvent.click(screen.getByRole("button", { name: "Скачать DOCX" }))

    await waitFor(() => expect(error).toHaveBeenCalled())
    expect(error.mock.calls[0]?.[1]).toMatchObject({
      description: "Раздел доступен администратору",
    })
    expect(urls.create).not.toHaveBeenCalled()
    expect(click).not.toHaveBeenCalled()
    // Состояние загрузки снимается: попытку можно повторить.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Скачать DOCX" })).toBeEnabled(),
    )
  })

  it("на 501 показывает тост, что сервер не поддерживает выгрузку, и не сохраняет файл", async () => {
    stubDraftExport({
      status: 501,
      body: { code: "NOT_IMPLEMENTED", message: "not implemented" },
    })
    const urls = stubObjectUrls()
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {})
    const error = vi.spyOn(toast, "error")

    render(<ProgramImportTab />)
    await uploadDraft()
    fireEvent.click(screen.getByRole("button", { name: "Скачать DOCX" }))

    await waitFor(() => expect(error).toHaveBeenCalled())
    expect(error.mock.calls[0]?.[1]).toMatchObject({
      description: "Сервер не поддерживает выгрузку",
    })
    expect(urls.create).not.toHaveBeenCalled()
    expect(click).not.toHaveBeenCalled()
  })

  it("показывает в карточке черновика кнопку «Добавить KSA» и скрытый file-input", async () => {
    stubKsaUpload()
    render(<ProgramImportTab />)
    await uploadDraft()

    const card = screen.getByRole("region", { name: "Черновик программы" })
    expect(within(card).getByRole("button", { name: "Добавить KSA" })).toBeEnabled()

    const input = within(card).getByLabelText("Файл KSA")
    expect(input).toHaveAttribute("type", "file")
    expect(input).toHaveAttribute("accept", ".pdf,.docx,.txt,.md")
    expect(input).toHaveClass("hidden")
  })

  it("загружает KSA multipart-запросом, обновляет таблицу и показывает тост", async () => {
    const token = makeToken({ sub: "admin", role: "admin", exp: 4102444800 })
    storeToken(token)
    stubKsaUpload()
    const success = vi.spyOn(toast, "success")

    render(<ProgramImportTab />)
    await uploadDraft()

    const ksaFile = selectKsaFile("ksa.docx")

    await waitFor(() =>
      expect(success).toHaveBeenCalledWith("Добавлено KSA: 1, уже было: 2"),
    )

    const ksaCall = fetchCalls().find((call) =>
      call.url.endsWith("/api/v1/admin/programs/drafts/draft-1/ksa"),
    )
    expect(ksaCall).toBeDefined()
    expect(ksaCall?.init.method).toBe("POST")
    const form = ksaCall?.init.body as FormData
    expect(form.get("file")).toBe(ksaFile)
    expect(new Headers(ksaCall?.init.headers).get("Authorization")).toBe(`Bearer ${token}`)

    // Карточка перечитана через getProgramDraft: счётчик и таблица обновились.
    expect(await screen.findByText("Компетенций: 2 · KSA: 3")).toBeInTheDocument()
    fireEvent.click(
      screen.getByRole("button", {
        name: "Показать KSA: Способен применять фундаментальные знания",
      }),
    )
    expect(screen.getByText("Теория вероятностей")).toBeInTheDocument()
    expect(screen.getByText("Основы математического анализа")).toBeInTheDocument()
    expect(screen.getByText("Решать прикладные задачи")).toBeInTheDocument()
  })

  it("показывает спиннер и блокирует кнопку, пока KSA загружаются", async () => {
    let release: (() => void) | undefined
    const gate = new Promise<void>((resolve) => {
      release = resolve
    })
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/admin/programs/drafts") && method === "GET") {
        return { status: 200, body: { items: [] } }
      }
      if (url.endsWith("/api/v1/admin/programs/import") && method === "POST") {
        return { status: 201, body: IMPORT_RESPONSE }
      }
      if (url.endsWith("/api/v1/admin/programs/drafts/draft-1/ksa") && method === "POST") {
        await gate
        return { status: 200, body: KSA_RESPONSE }
      }
      if (url.endsWith("/api/v1/admin/programs/drafts/draft-1") && method === "GET") {
        return { status: 200, body: UPDATED_DRAFT_DETAIL }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    const success = vi.spyOn(toast, "success")

    render(<ProgramImportTab />)
    await uploadDraft()

    selectKsaFile()
    const button = screen.getByRole("button", { name: "Добавить KSA" })
    await waitFor(() => expect(button).toBeDisabled())
    expect(button).toHaveAttribute("aria-busy", "true")
    expect(button.querySelector("svg.animate-spin")).not.toBeNull()

    release?.()
    await waitFor(() =>
      expect(success).toHaveBeenCalledWith("Добавлено KSA: 1, уже было: 2"),
    )
    await waitFor(() => expect(button).toBeEnabled())
    expect(button).toHaveAttribute("aria-busy", "false")
  })

  it("на 422 показывает понятную ошибку извлечения KSA и не перечитывает черновик", async () => {
    stubKsaUpload({
      status: 422,
      body: { code: "KSA_EXTRACT_FAILED", message: "не удалось извлечь KSA" },
    })
    const error = vi.spyOn(toast, "error")

    render(<ProgramImportTab />)
    await uploadDraft()
    selectKsaFile()

    await waitFor(() => expect(error).toHaveBeenCalledTimes(1))
    expect(error.mock.calls[0]?.[0]).toBe("Не удалось извлечь KSA")
    expect(error.mock.calls[0]?.[1]?.description).toContain("не удалось разобрать")

    expect(
      fetchCalls().some(
        (call) =>
          call.url.endsWith("/drafts/draft-1") && (call.init.method ?? "GET") === "GET",
      ),
    ).toBe(false)
    // Состояние загрузки снимается: попытку можно повторить.
    await waitFor(() => expect(screen.getByRole("button", { name: "Добавить KSA" })).toBeEnabled())
  })

  it("на 503 сообщает, что LLM недоступна", async () => {
    stubKsaUpload({
      status: 503,
      body: { code: "LLM_UNAVAILABLE", message: "llm unavailable" },
    })
    const error = vi.spyOn(toast, "error")

    render(<ProgramImportTab />)
    await uploadDraft()
    selectKsaFile()

    await waitFor(() => expect(error).toHaveBeenCalledTimes(1))
    expect(error.mock.calls[0]?.[0]).toBe("LLM недоступна")
    expect(error.mock.calls[0]?.[1]?.description).toContain("временно недоступен")
  })

  it("на 501 сообщает, что сервер не поддерживает добавление KSA", async () => {
    stubKsaUpload({
      status: 501,
      body: { code: "NOT_IMPLEMENTED", message: "not implemented" },
    })
    const error = vi.spyOn(toast, "error")

    render(<ProgramImportTab />)
    await uploadDraft()
    selectKsaFile()

    await waitFor(() => expect(error).toHaveBeenCalledTimes(1))
    expect(error.mock.calls[0]?.[0]).toBe("Добавление KSA не поддерживается")
    expect(error.mock.calls[0]?.[1]?.description).toContain("функция ещё не включена")
  })

  it("при unknown_codes показывает предупреждение только с кодами компетенций", async () => {
    stubKsaUpload({
      status: 200,
      body: {
        draft_id: "draft-1",
        added: 1,
        already_present: 0,
        unknown_codes: ["ОПК-9", "ПК-7"],
        warnings: [],
      },
    })
    const warning = vi.spyOn(toast, "warning")

    render(<ProgramImportTab />)
    await uploadDraft()
    selectKsaFile()

    await waitFor(() => expect(warning).toHaveBeenCalledTimes(1))
    expect(warning.mock.calls[0]?.[0]).toBe("Неизвестные коды компетенций")
    const description = warning.mock.calls[0]?.[1]?.description
    expect(description).toBe("ОПК-9, ПК-7")
    expect(description).not.toContain("draft-1")
  })

  it("повторная загрузка не дублирует KSA и обновляет карточку по already_present", async () => {
    const responses = [
      KSA_RESPONSE,
      { draft_id: "draft-1", added: 0, already_present: 3, unknown_codes: [], warnings: [] },
    ]
    let uploadIndex = 0
    stubKsaUpload(() => ({
      status: 200,
      body: responses[Math.min(uploadIndex++, responses.length - 1)],
    }))
    const success = vi.spyOn(toast, "success")

    render(<ProgramImportTab />)
    await uploadDraft()

    selectKsaFile("first.docx")
    await waitFor(() =>
      expect(success).toHaveBeenCalledWith("Добавлено KSA: 1, уже было: 2"),
    )
    await waitFor(() => expect(screen.getByRole("button", { name: "Добавить KSA" })).toBeEnabled())

    selectKsaFile("second.docx")
    await waitFor(() =>
      expect(success).toHaveBeenCalledWith("Добавлено KSA: 0, уже было: 3"),
    )
    await waitFor(() => expect(screen.getByRole("button", { name: "Добавить KSA" })).toBeEnabled())

    // Каждая успешная загрузка перечитывает черновик, а не дописывает KSA на клиенте.
    const detailCalls = fetchCalls().filter(
      (call) =>
        call.url.endsWith("/drafts/draft-1") && (call.init.method ?? "GET") === "GET",
    )
    expect(detailCalls).toHaveLength(2)
    expect(screen.getByText("Компетенций: 2 · KSA: 3")).toBeInTheDocument()

    fireEvent.click(
      screen.getByRole("button", {
        name: "Показать KSA: Способен применять фундаментальные знания",
      }),
    )
    expect(screen.getAllByText("Основы математического анализа")).toHaveLength(1)
    expect(screen.getAllByText("Решать прикладные задачи")).toHaveLength(1)
    expect(screen.getAllByText("Теория вероятностей")).toHaveLength(1)
  })
})
