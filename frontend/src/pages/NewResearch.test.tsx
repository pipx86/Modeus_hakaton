/// <reference types="@testing-library/jest-dom" />

import { fireEvent, render, screen } from "@testing-library/react"
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom"
import { expect, test } from "vitest"
import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { NewResearch } from "./NewResearch"

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

test("рендерит форму профессии, блок источников и шаги процесса", () => {
  setFetchHandler(async () => ({ status: 200, body: [] }))
  renderPage()
  expect(screen.getByText("Новое исследование")).toBeInTheDocument()
  expect(screen.getByLabelText("Профессия / роль")).toBeInTheDocument()
  expect(screen.getByText("Источники к прогону")).toBeInTheDocument()
  expect(screen.getByText("Как это работает")).toBeInTheDocument()
  expect(screen.getByText(/Собирается информация о реальной деятельности/)).toBeInTheDocument()
  expect(screen.getByText(/Строится дерево/)).toBeInTheDocument()
})

test("поле профессии связано со списком подсказок ролей", () => {
  setFetchHandler(async () => ({ status: 200, body: [] }))
  const { container } = renderPage()
  const input = screen.getByLabelText("Профессия / роль")
  expect(input).toHaveAttribute("list", "role-options")
  const options = container.querySelectorAll("#role-options option")
  expect(options.length).toBeGreaterThan(0)
  const values = Array.from(options).map((option) => option.getAttribute("value"))
  expect(values).toContain("Data Scientist")
})

test("показывает ошибку валидации профессии после пустого сабмита и лимит длины", () => {
  setFetchHandler(async () => ({ status: 200, body: [] }))
  const { container } = renderPage()
  fireEvent.submit(container.querySelector("form")!)
  expect(screen.getByText("Укажите профессию или роль")).toBeInTheDocument()
  fireEvent.change(screen.getByLabelText("Профессия / роль"), {
    target: { value: "а".repeat(121) },
  })
  expect(screen.getByText("Не длиннее 120 символов")).toBeInTheDocument()
})

test("черновик источника нельзя добавить без названия; лимит названия работает", () => {
  setFetchHandler(async () => ({ status: 200, body: [] }))
  renderPage()
  const add = screen.getByRole("button", { name: "Добавить источник" })
  expect(add).toBeDisabled()
  fireEvent.change(screen.getByLabelText("Название"), { target: { value: "а".repeat(201) } })
  expect(screen.getByText("Не длиннее 200 символов")).toBeInTheDocument()
})

test("добавляет и удаляет черновик источника с меткой типа", () => {
  setFetchHandler(async () => ({ status: 200, body: [] }))
  renderPage()
  fireEvent.change(screen.getByLabelText("Тип"), { target: { value: "vacancy" } })
  fireEvent.change(screen.getByLabelText("Название"), { target: { value: "Тестовая вакансия" } })
  fireEvent.click(screen.getByRole("button", { name: "Добавить источник" }))
expect(screen.getByText("Тестовая вакансия")).toBeInTheDocument()
expect(screen.getByText("· Вакансия")).toBeInTheDocument()
  fireEvent.click(screen.getByRole("button", { name: "Удалить черновик Тестовая вакансия" }))
  expect(screen.queryByText("Тестовая вакансия")).not.toBeInTheDocument()
})

test("запуск прогона: сначала источники, затем run, затем переход на страницу run", async () => {
  happyHandler()
  renderPage()
  fireEvent.change(screen.getByLabelText("Профессия / роль"), {
    target: { value: "Data Scientist" },
  })
  fireEvent.change(screen.getByLabelText("Тип"), { target: { value: "vacancy" } })
  fireEvent.change(screen.getByLabelText("Название"), { target: { value: "Тестовая вакансия" } })
  fireEvent.click(screen.getByRole("button", { name: "Добавить источник" }))
  fireEvent.click(screen.getByRole("button", { name: "Начать исследование" }))

  const probe = await screen.findByTestId("location")
  expect(probe).toHaveTextContent("/research/run-42")

  const calls = fetchCalls()
  const sourcePost = calls.find(
    (call) => call.url.endsWith("/api/v1/sources") && call.init.method === "POST",
  )
  const runPost = calls.find(
    (call) => call.url.endsWith("/api/v1/runs") && call.init.method === "POST",
  )
  expect(sourcePost).toBeDefined()
  expect(runPost).toBeDefined()
  expect(JSON.parse(String(sourcePost!.init.body))).toMatchObject({
    type: "vacancy",
    title: "Тестовая вакансия",
  })
  expect(JSON.parse(String(runPost!.init.body))).toEqual({ profession: "Data Scientist" })
  expect(calls.length).toBeGreaterThanOrEqual(3)
})

test("ошибка API при создании источника показывается инлайном", async () => {
  setFetchHandler(async (url, init) => {
    const method = init?.method ?? "GET"
    if (url.endsWith("/api/v1/sources") && method === "GET") return { status: 200, body: [] }
    if (url.endsWith("/api/v1/sources") && method === "POST") {
      return { status: 422, body: { code: "VALIDATION", message: "title: слишком длинное название" } }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
  renderPage()
  fireEvent.change(screen.getByLabelText("Профессия / роль"), { target: { value: "Data Scientist" } })
  fireEvent.change(screen.getByLabelText("Название"), { target: { value: "Черновик" } })
  fireEvent.click(screen.getByRole("button", { name: "Добавить источник" }))
  fireEvent.click(screen.getByRole("button", { name: "Начать исследование" }))
  expect(await screen.findByText("title: слишком длинное название")).toBeInTheDocument()
})

test("сетевой сбой показывает сообщение о недоступности сервера", async () => {
  setFetchHandler(async (_url, init) => {
    const method = init?.method ?? "GET"
    if (method === "GET") return { status: 200, body: [] }
    throw new Error("offline")
  })
  renderPage()
  fireEvent.change(screen.getByLabelText("Профессия / роль"), { target: { value: "Data Scientist" } })
  fireEvent.click(screen.getByRole("button", { name: "Начать исследование" }))
  expect(
    await screen.findByText(
      "Сервер недоступен. Проверьте, что бэкенд или мок запущен (npm run mock).",
    ),
  ).toBeInTheDocument()
})

test("при недоступном сервере блок источников показывает короткую пометку", () => {
  setFetchHandler(async () => {
    throw new Error("offline")
  })
  renderPage()
  expect(screen.getByText("Сервер недоступен")).toBeInTheDocument()
})