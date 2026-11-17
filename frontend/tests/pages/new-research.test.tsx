import { act, fireEvent, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom"
import { describe, expect, it } from "vitest"
import { setFetchHandler } from "@/test/fetch-stub"
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
    if (url.endsWith("/api/v1/sources") && method === "GET") {
      return { status: 200, body: [] }
    }
    if (url.endsWith("/api/v1/sources") && method === "POST") {
      return { status: 201, body: { id: "s-1", type: "vacancy", title: "Тестовая вакансия" } }
    }
    if (url.endsWith("/api/v1/runs") && method === "POST") {
      return { status: 201, body: { id: "run-42", status: "pending", profession: "Data Scientist" } }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
}

describe("NewResearch", () => {
  it("рендерит форму, блок источников и шаги процесса", async () => {
    setFetchHandler(async () => ({ status: 200, body: [] }))
    renderPage()
    await act(async () => {})

    expect(screen.getByText("Новое исследование")).toBeInTheDocument()
    expect(screen.getByLabelText("Профессия / роль")).toBeInTheDocument()
    expect(screen.getByText("Источники к прогону")).toBeInTheDocument()
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

  it("черновик источника нельзя добавить без названия, лимит названия работает", async () => {
    setFetchHandler(async () => ({ status: 200, body: [] }))
    renderPage()
    await act(async () => {})

    expect(screen.getByRole("button", { name: "Добавить источник" })).toBeDisabled()

    fireEvent.change(screen.getByLabelText("Название"), { target: { value: "а".repeat(201) } })
    expect(screen.getByText("Не длиннее 200 символов")).toBeInTheDocument()
  })

  it("добавляет и удаляет черновик источника с меткой типа", async () => {
    setFetchHandler(async () => ({ status: 200, body: [] }))
    renderPage()
    await act(async () => {})
    const user = userEvent.setup()

    // Radix Select — кнопка с попапом, поэтому не fireEvent.change, а два клика.
    await user.click(screen.getByLabelText("Тип"))
    await user.click(await screen.findByRole("option", { name: "Вакансия" }))

    fireEvent.change(screen.getByLabelText("Название"), { target: { value: "Тестовая вакансия" } })
    fireEvent.click(screen.getByRole("button", { name: "Добавить источник" }))

    expect(screen.getByText("Тестовая вакансия")).toBeInTheDocument()
    expect(screen.getByText("Тестовая вакансия").closest("li")).toHaveTextContent("· Вакансия")

    fireEvent.click(screen.getByRole("button", { name: "Удалить черновик Тестовая вакансия" }))
    expect(screen.queryByText("Тестовая вакансия")).not.toBeInTheDocument()
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

  it("показывает ошибку API при создании источника", async () => {
    setFetchHandler(async (url, init) => {
      const method = init?.method ?? "GET"
      if (url.endsWith("/api/v1/sources") && method === "GET") return { status: 200, body: [] }
      if (url.endsWith("/api/v1/sources") && method === "POST") {
        return { status: 422, body: { code: "VALIDATION", message: "title: слишком длинное название" } }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    renderPage()
    await act(async () => {})

    fireEvent.change(screen.getByLabelText("Профессия / роль"), { target: { value: "Data Scientist" } })
    fireEvent.change(screen.getByLabelText("Название"), { target: { value: "Черновик" } })
    fireEvent.click(screen.getByRole("button", { name: "Добавить источник" }))
    fireEvent.click(screen.getByRole("button", { name: "Начать исследование" }))

    expect(await screen.findByText("title: слишком длинное название")).toBeInTheDocument()
  })

  it("сетевой сбой показывает сообщение о недоступности сервера", async () => {
    setFetchHandler(async (_url, init) => {
      if ((init?.method ?? "GET") === "GET") return { status: 200, body: [] }
      throw new Error("offline")
    })
    renderPage()
    await act(async () => {})

    fireEvent.change(screen.getByLabelText("Профессия / роль"), { target: { value: "Data Scientist" } })
    fireEvent.click(screen.getByRole("button", { name: "Начать исследование" }))

    expect(
      await screen.findByText(
        "Сервер недоступен. Проверьте, что бэкенд или мок запущен (npm run mock).",
      ),
    ).toBeInTheDocument()
  })

  it("при недоступном сервере блок источников показывает короткую пометку", async () => {
    setFetchHandler(async () => {
      throw new Error("offline")
    })
    renderPage()
    await act(async () => {})

    expect(screen.getByText("Сервер недоступен")).toBeInTheDocument()
  })
})
