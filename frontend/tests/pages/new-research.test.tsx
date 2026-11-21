import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom"
import { describe, expect, it } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { NewResearch } from "@/pages/NewResearch"

function LocationProbe() {
  const location = useLocation()
  return <div data-testid="location">{location.pathname}</div>
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/research/new"]}>
      <Routes>
        <Route path="/research/new" element={<NewResearch />} />
        <Route path="/research/:id" element={<LocationProbe />} />
        <Route path="/" element={<LocationProbe />} />
      </Routes>
    </MemoryRouter>,
  )
}

function happyHandler() {
  return setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.endsWith("/api/v1/runs") && method === "POST") {
      return { status: 201, body: { id: "run-42", status: "pending", profession: "Data Scientist" } }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

describe("NewResearch", () => {
  it("рендерит форму и шаги процесса без блока загрузки источников", async () => {
    setFetchHandler(async () => ({ status: 200, body: [] }))
    renderPage()
    await act(async () => {})

    expect(screen.getByText("Новое исследование")).toBeInTheDocument()
    expect(screen.getByLabelText("Профессия / роль")).toBeInTheDocument()
    expect(screen.queryByText("Источники к прогону")).not.toBeInTheDocument()
    expect(screen.queryByText("Добавить источник")).not.toBeInTheDocument()
    expect(screen.getByText("Как это работает")).toBeInTheDocument()
    expect(screen.getByText(/Собирается информация о реальной деятельности/)).toBeInTheDocument()
    expect(screen.getByText(/Строится дерево/)).toBeInTheDocument()
  })

  it("показывает подсказки профессий и подставляет выбранную", async () => {
    setFetchHandler(async () => ({ status: 200, body: [] }))
    renderPage()
    await act(async () => {})

    const input = screen.getByLabelText("Профессия / роль")
    fireEvent.focus(input)
    fireEvent.click(screen.getByRole("button", { name: "Аналитик данных" }))

    expect(input).toHaveValue("Аналитик данных")
  })

  it("фильтрует подсказки по введённому тексту", async () => {
    setFetchHandler(async () => ({ status: 200, body: [] }))
    renderPage()
    await act(async () => {})

    fireEvent.change(screen.getByLabelText("Профессия / роль"), { target: { value: "DevOps" } })

    expect(screen.getByRole("button", { name: "DevOps-инженер" })).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Аналитик данных" })).not.toBeInTheDocument()
  })

  it("подсвечивает уровень по клику и подставляет его при выборе строки", async () => {
    setFetchHandler(async () => ({ status: 200, body: [] }))
    renderPage()
    await act(async () => {})

    const input = screen.getByLabelText("Профессия / роль")
    fireEvent.focus(input)

    const middle = screen.getByRole("button", { name: "middle" })
    fireEvent.click(middle)

    expect(middle).toHaveAttribute("aria-pressed", "true")
    expect(input).toHaveValue("")
    expect(screen.getByRole("button", { name: "junior" })).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Backend разработчик" }))

    expect(input).toHaveValue("Backend разработчик (middle)")
  })

  it("находит Backend по уровню в фильтре", async () => {
    setFetchHandler(async () => ({ status: 200, body: [] }))
    renderPage()
    await act(async () => {})

    fireEvent.change(screen.getByLabelText("Профессия / роль"), { target: { value: "senior" } })

    expect(screen.getByRole("button", { name: "Backend разработчик" })).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Аналитик данных" })).not.toBeInTheDocument()
  })

  it("показывает ошибку после пустого сабмита и следит за лимитом длины", async () => {
    setFetchHandler(async () => ({ status: 200, body: [] }))
    const { container } = renderPage()
    await act(async () => {})

    fireEvent.submit(container.querySelector("form")!)
    expect(screen.getByText("Укажите профессию или роль")).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText("Профессия / роль"), {
      target: { value: "а".repeat(121) },
    })
    expect(screen.getByText("Не длиннее 120 символов")).toBeInTheDocument()
  })

  it("запускает прогон и переходит на его страницу", async () => {
    happyHandler()
    renderPage()
    await act(async () => {})

    fireEvent.change(screen.getByLabelText("Профессия / роль"), {
      target: { value: "Data Scientist" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Начать исследование" }))

    const probe = await screen.findByTestId("location")
    expect(probe).toHaveTextContent("/research/run-42")
  })

  it("показывает ошибку API при запуске прогона", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/runs") && method === "POST") {
        return { status: 422, body: { code: "VALIDATION", message: "profession: слишком длинное название" } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    renderPage()
    await act(async () => {})

    fireEvent.change(screen.getByLabelText("Профессия / роль"), { target: { value: "Data Scientist" } })
    fireEvent.click(screen.getByRole("button", { name: "Начать исследование" }))

    expect(await screen.findByText("profession: слишком длинное название")).toBeInTheDocument()
  })

  it("сетевой сбой показывает сообщение о недоступности сервера", async () => {
    setFetchHandler(async () => {
      throw new Error("offline")
    })
    renderPage()
    await act(async () => {})

    fireEvent.change(screen.getByLabelText("Профессия / роль"), { target: { value: "Data Scientist" } })
    fireEvent.click(screen.getByRole("button", { name: "Начать исследование" }))

    expect(
      await screen.findByText(
        "Сервер недоступен. Попробуйте ещё раз.",
      ),
    ).toBeInTheDocument()
  })

  it("сбрасывает выбранный уровень при выборе другой роли", async () => {
    setFetchHandler(async () => ({ status: 200, body: [] }))
    renderPage()
    await act(async () => {})

    const input = screen.getByLabelText("Профессия / роль")
    fireEvent.focus(input)
    fireEvent.click(screen.getByRole("button", { name: "middle" }))
    fireEvent.click(screen.getByRole("button", { name: "Аналитик данных" }))

    expect(input).toHaveValue("Аналитик данных")
  })

  it("создаёт исследование профессии без запроса оценки стоимости", async () => {
    happyHandler()
    renderPage()
    expect(screen.queryByRole("button", { name: "План обучения" })).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText("Профессия / роль"), { target: { value: "Аналитик" } })
    fireEvent.click(screen.getByRole("button", { name: "Начать исследование" }))
    expect(await screen.findByTestId("location")).toHaveTextContent("/research/run-42")
    const request = fetchCalls().find((call) => call.init.method === "POST")
    expect(JSON.parse(String(request?.init.body))).toEqual({
      profession: "Аналитик",
      type: "chat",
      result_kind: "activity",
    })
    expect(fetchCalls().some((call) => call.url.includes("/runs/estimate"))).toBe(false)
  })
})

const PROGRAMS = [
  { id: "p-1", title: "Прикладная математика", code: "01.03.02" },
  { id: "p-2", title: "Программная инженерия" },
]

/** Обработчик запуска и списка программ для карточки 25. */
function programHandler(programs: unknown[] = PROGRAMS) {
  return setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.endsWith("/api/v1/academic/programs") && method === "GET") {
      return { status: 200, body: programs }
    }
    if (url.endsWith("/api/v1/runs") && method === "POST") {
      return { status: 201, body: { id: "run-42", status: "pending", profession: "Data Scientist" } }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

/** Переключает форму в режим сопоставления и ждёт загрузку списка программ. */
async function switchToProgram() {
  fireEvent.click(
    screen.getByRole("radio", {
      name: /Описание и сопоставление с образовательной программой/,
    }),
  )
  expect(await screen.findByText("Образовательная программа")).toBeInTheDocument()
  await waitFor(() =>
    expect(screen.queryByText(/Загружаем список программ/)).not.toBeInTheDocument(),
  )
}

describe("NewResearch: ожидаемый результат", () => {
  it("по умолчанию выбирает описание деятельности и не запрашивает программы", async () => {
    programHandler()
    renderPage()
    await act(async () => {})

    expect(screen.getByRole("radio", { name: /Описание деятельности/ })).toBeChecked()
    expect(screen.queryByText("Образовательная программа")).not.toBeInTheDocument()
    expect(fetchCalls().some((call) => call.url.includes("/academic/programs"))).toBe(false)
  })

  it("в режиме сопоставления показывает выбор программы и отправляет program_id", async () => {
    programHandler()
    const user = userEvent.setup()
    renderPage()
    await act(async () => {})

    fireEvent.change(screen.getByLabelText("Профессия / роль"), {
      target: { value: "Data Scientist" },
    })
    await switchToProgram()

    await user.click(screen.getByLabelText("Образовательная программа"))
    await user.click(
      await screen.findByRole("option", { name: "01.03.02 · Прикладная математика" }),
    )
    fireEvent.click(screen.getByRole("button", { name: "Начать исследование" }))

    expect(await screen.findByTestId("location")).toHaveTextContent("/research/run-42")
    const request = fetchCalls().find((call) => call.init.method === "POST")
    expect(JSON.parse(String(request?.init.body))).toMatchObject({
      result_kind: "program",
      program_id: "p-1",
    })
  })

  it("блокирует запуск без выбранной программы и объясняет причину", async () => {
    programHandler()
    renderPage()
    await act(async () => {})

    fireEvent.change(screen.getByLabelText("Профессия / роль"), {
      target: { value: "Data Scientist" },
    })
    await switchToProgram()

    expect(screen.getByRole("button", { name: "Начать исследование" })).toBeDisabled()
    expect(
      screen.getByText("Чтобы запустить исследование, выберите образовательную программу."),
    ).toBeInTheDocument()
    expect(fetchCalls().some((call) => call.init.method === "POST")).toBe(false)
  })

  it("аккуратно показывает программы без title/code и отбрасывает записи без id", async () => {
    programHandler([
      { id: "p-1", name: "Только name" },
      { title: "Без id" },
      { id: "p-2", title: "С кодом", code: "09.03.01" },
    ])
    const user = userEvent.setup()
    renderPage()
    await act(async () => {})

    await switchToProgram()
    await user.click(screen.getByLabelText("Образовательная программа"))

    expect(await screen.findByRole("option", { name: "Только name" })).toBeInTheDocument()
    expect(screen.getByRole("option", { name: "09.03.01 · С кодом" })).toBeInTheDocument()
    expect(screen.queryByRole("option", { name: /Без id/ })).not.toBeInTheDocument()
  })

  it("показывает ошибку загрузки программ и не даёт запустить исследование", async () => {
    setFetchHandler(async (url) => {
      if (url.includes("/academic/programs")) {
        return { status: 500, body: { code: "INTERNAL", message: "программы недоступны" } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    renderPage()
    await act(async () => {})

    fireEvent.change(screen.getByLabelText("Профессия / роль"), {
      target: { value: "Data Scientist" },
    })
    await switchToProgram()

    expect(await screen.findByText("программы недоступны")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Повторить" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Начать исследование" })).toBeDisabled()
    expect(fetchCalls().some((call) => call.init.method === "POST")).toBe(false)
  })

  it("при возврате к описанию деятельности не отправляет program_id", async () => {
    programHandler()
    renderPage()
    await act(async () => {})

    fireEvent.change(screen.getByLabelText("Профессия / роль"), {
      target: { value: "Data Scientist" },
    })
    await switchToProgram()
    fireEvent.click(screen.getByRole("radio", { name: /Описание деятельности/ }))
    fireEvent.click(screen.getByRole("button", { name: "Начать исследование" }))

    expect(await screen.findByTestId("location")).toHaveTextContent("/research/run-42")
    const request = fetchCalls().find((call) => call.init.method === "POST")
    const body = JSON.parse(String(request?.init.body)) as Record<string, unknown>
    expect(body.result_kind).toBe("activity")
    expect(body).not.toHaveProperty("program_id")
  })
})
