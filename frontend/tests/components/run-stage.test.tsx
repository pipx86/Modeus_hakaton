import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { RunStageStepper } from "@/components/run-stage"

describe("RunStageStepper", () => {
  it("показывает этапы и подсказку для интервью", () => {
    render(<RunStageStepper stage="interview" />)

    expect(screen.getByLabelText("Этапы исследования")).toBeInTheDocument()
    expect(screen.getByText("Интервью")).toBeInTheDocument()
    expect(screen.getByText("Агент-интервьюер задаёт вопросы эксперту")).toBeInTheDocument()
  })

  it("подсказывает, что делает аналитик", () => {
    render(<RunStageStepper stage="analysis" />)

    expect(
      screen.getByText("Аналитик разбирает ответы и ищет подтверждения"),
    ).toBeInTheDocument()
  })

  it("для завершённого прогона скрывает подсказку", () => {
    render(<RunStageStepper stage="done" />)

    expect(screen.queryByText(/Агент-интервьюер|Аналитик разбирает/)).not.toBeInTheDocument()
  })

  it("помечает ошибку при failed", () => {
    render(<RunStageStepper stage="analysis" failed />)

    expect(screen.getByText("Ошибка")).toBeInTheDocument()
    expect(screen.queryByText("Аналитик разбирает ответы и ищет подтверждения")).not.toBeInTheDocument()
  })
})
