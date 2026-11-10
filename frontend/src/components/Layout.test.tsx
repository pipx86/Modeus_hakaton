/// <reference types="@testing-library/jest-dom" />

import { fireEvent, render, screen, within } from "@testing-library/react"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { expect, test } from "vitest"
import { Layout } from "./Layout"

function renderLayout(initialPath: string) {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<div>page-dashboard</div>} />
          <Route path="/research/new" element={<div>page-new</div>} />
          <Route path="/research/:id" element={<div>page-research</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  )
}

test("сайдбар содержит навигацию и управляющие кнопки", () => {
  renderLayout("/")
  expect(screen.getByRole("link", { name: "Исследования" })).toBeInTheDocument()
  expect(screen.getByRole("link", { name: "Новое исследование" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Свернуть сайдбар" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Открыть меню" })).toBeInTheDocument()
})

test("хлебные крошки на /research/new показывают путь и текущую страницу", () => {
  renderLayout("/research/new")
  const header = within(screen.getByRole("banner"))
  expect(header.getByRole("link", { name: "Исследования" })).toBeInTheDocument()
  expect(header.getByText("Новое исследование")).toBeInTheDocument()
})

test("клик по пункту навигации переключает содержимое Outlet", () => {
  renderLayout("/")
  expect(screen.getByText("page-dashboard")).toBeInTheDocument()
  fireEvent.click(screen.getByRole("link", { name: "Новое исследование" }))
  expect(screen.getByText("page-new")).toBeInTheDocument()
})