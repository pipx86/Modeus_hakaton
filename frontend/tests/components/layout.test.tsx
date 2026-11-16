import { fireEvent, render, screen, within } from "@testing-library/react"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { describe, expect, it } from "vitest"
import { Layout } from "@/components/Layout"

function renderLayout(initialPath = "/") {
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

describe("Layout", () => {
  it("показывает навигацию и управляющие кнопки", () => {
    renderLayout("/")

    expect(screen.getByRole("link", { name: "Исследования" })).toBeInTheDocument()
    expect(screen.getByRole("link", { name: "Новое исследование" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Свернуть сайдбар" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Открыть меню" })).toBeInTheDocument()
  })

  it("на / показывает одну крошку", () => {
    renderLayout("/")
    const header = within(screen.getByRole("banner"))
    expect(header.getByText("Исследования")).toBeInTheDocument()
  })

  it("на /research/new строит путь из двух крошек", () => {
    renderLayout("/research/new")
    const header = within(screen.getByRole("banner"))
    expect(header.getByRole("link", { name: "Исследования" })).toBeInTheDocument()
    expect(header.getByText("Новое исследование")).toBeInTheDocument()
  })

  it("на странице исследования вторая крошка называется «Исследование»", () => {
    renderLayout("/research/42")
    const header = within(screen.getByRole("banner"))
    expect(header.getByText("Исследование")).toBeInTheDocument()
  })

  it("клик по навигации меняет содержимое Outlet", () => {
    renderLayout("/")
    expect(screen.getByText("page-dashboard")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("link", { name: "Новое исследование" }))

    expect(screen.getByText("page-new")).toBeInTheDocument()
  })

  it("сворачивает и разворачивает сайдбар", () => {
    renderLayout("/")

    fireEvent.click(screen.getByRole("button", { name: "Свернуть сайдбар" }))
    expect(screen.getByRole("button", { name: "Развернуть сайдбар" })).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Развернуть сайдбар" }))
    expect(screen.getByRole("button", { name: "Свернуть сайдбар" })).toBeInTheDocument()
  })
})
