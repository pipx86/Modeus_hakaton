/// <reference types="@testing-library/jest-dom" />

import { render, screen, waitFor } from "@testing-library/react"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { expect, test } from "vitest"
import { setFetchHandler } from "@/test/fetch-stub"
import { Research } from "./Research"

function renderPage(runId = "7") {
  return render(
    <MemoryRouter initialEntries={[`/research/${runId}`]}>
      <Routes>
        <Route path="/research/:id" element={<Research />} />
      </Routes>
    </MemoryRouter>,
  )
}

test("при недоступном бэкенде показывает баннер ошибки и кнопку повтора", async () => {
  setFetchHandler(async () => {
    throw new Error("offline")
  })
  renderPage()
  expect(
    await screen.findByText("Бэкенд недоступен по адресу из VITE_API_URL"),
  ).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Повторить" })).toBeInTheDocument()
})

test("шапка показывает профессию, а вкладка источников — список с сервера", async () => {
  setFetchHandler(async (url) => {
    if (url.includes("/api/v1/runs/7/tree")) return { status: 200, body: [] }
    if (url.includes("/api/v1/runs/7")) {
      return {
        status: 200,
        body: { id: "7", status: "done", profession: "Data Scientist", started_at: "2026-11-06T10:00:00Z" },
      }
    }
    if (url.endsWith("/api/v1/sources")) {
      return {
        status: 200,
        body: [{ id: "s-1", type: "vacancy", title: "Вакансия DS, HH", created_at: "2026-11-06T09:00:00Z" }],
      }
    }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
  renderPage()
  expect(await screen.findByRole("heading", { name: "Data Scientist" })).toBeInTheDocument()
  expect(await screen.findByText("Вакансия DS, HH")).toBeInTheDocument()
})

test("пустое дерево показывает подсказку ожидания на вкладке «Дерево»", async () => {
  setFetchHandler(async (url) => {
    if (url.includes("/api/v1/runs/7/tree")) return { status: 200, body: [] }
    if (url.includes("/api/v1/runs/7")) {
      return { status: 200, body: { id: "7", status: "running", profession: "Data Scientist" } }
    }
    if (url.endsWith("/api/v1/sources")) return { status: 200, body: [] }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
  renderPage()
  await waitFor(() =>
    expect(screen.getByRole("heading", { name: "Data Scientist" })).toBeInTheDocument(),
  )

  // Radix Tabs (activationMode="automatic") выбирает вкладку по фокусу,
  // а jsdom не перемещает фокус при клике — поэтому focus(), не click().
  screen.getByRole("tab", { name: "Дерево" }).focus()

  expect(await screen.findByText("Дерево ещё не построено")).toBeInTheDocument()
})
test("страница исследования показывает вкладку чата", async () => {
  setFetchHandler(async (url) => {
    if (url.includes("/api/v1/runs/7")) {
      return { status: 200, body: { id: "7", status: "running", profession: "Data Scientist" } }
    }
    if (url.endsWith("/api/v1/sources")) return { status: 200, body: [] }
    if (url.includes("/api/v1/runs/7/tree")) return { status: 200, body: [] }
    if (url.endsWith("/api/v1/runs/7/messages")) return { status: 200, body: [] }
    return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
  })
  renderPage()
  expect(await screen.findByRole("tab", { name: "Чат" })).toBeInTheDocument()
})