import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { StatusBadge } from "@/components/status-badge"

describe("StatusBadge", () => {
  it("показывает «Ожидание» для pending", () => {
    render(<StatusBadge status="pending" />)
    expect(screen.getByText("Ожидание")).toBeInTheDocument()
  })

  it("показывает «Интервью» для running", () => {
    render(<StatusBadge status="running" />)
    expect(screen.getByText("Интервью")).toBeInTheDocument()
  })

  it("показывает «Завершено» для done", () => {
    render(<StatusBadge status="done" />)
    expect(screen.getByText("Завершено")).toBeInTheDocument()
  })

  it("показывает «Ошибка» для failed", () => {
    render(<StatusBadge status="failed" />)
    expect(screen.getByText("Ошибка")).toBeInTheDocument()
  })

  it("без статуса падает в pending", () => {
    render(<StatusBadge />)
    expect(screen.getByText("Ожидание")).toBeInTheDocument()
  })

  it("красит бейдж по статусу", () => {
    const { container, rerender } = render(<StatusBadge status="done" />)
    expect(container.firstElementChild).toHaveClass("bg-success/15")
    expect(container.firstElementChild).toHaveClass("text-success")

    rerender(<StatusBadge status="failed" />)
    expect(container.firstElementChild).toHaveClass("bg-destructive/15")
  })

  it("не рисует точку-маркер", () => {
    const { container } = render(<StatusBadge status="running" />)
    const dot = container.querySelector("span.size-1\\.5")
    expect(dot).toBeNull()
  })
})
