import { fireEvent, render, screen } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { ErrorBoundary } from "@/components/error-boundary"

function Bomb({ shouldThrow }: { shouldThrow: boolean }) {
  if (shouldThrow) throw new Error("boom")
  return <div>Всё хорошо</div>
}

describe("ErrorBoundary", () => {
  beforeEach(() => {
    // React печатает stack trace упавшего компонента — в тесте это шум.
    vi.spyOn(console, "error").mockImplementation(() => {})
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it("показывает экран ошибки вместо белого полотна", () => {
    render(
      <ErrorBoundary>
        <Bomb shouldThrow />
      </ErrorBoundary>,
    )

    expect(screen.getByText("Что-то пошло не так")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Попробовать снова" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Обновить страницу" })).toBeInTheDocument()
  })

  it("inline-вариант не занимает весь экран", () => {
    render(
      <ErrorBoundary variant="inline">
        <Bomb shouldThrow />
      </ErrorBoundary>,
    )

    const title = screen.getByText("Что-то пошло не так")
    expect(title).toBeInTheDocument()
    expect(title.closest(".min-h-dvh")).toBeNull()
  })

  it("после сброса рендерит рабочее содержимое", () => {
    const { rerender } = render(
      <ErrorBoundary>
        <Bomb shouldThrow />
      </ErrorBoundary>,
    )

    rerender(
      <ErrorBoundary>
        <Bomb shouldThrow={false} />
      </ErrorBoundary>,
    )
    fireEvent.click(screen.getByRole("button", { name: "Попробовать снова" }))

    expect(screen.getByText("Всё хорошо")).toBeInTheDocument()
  })
})
