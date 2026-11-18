import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { Donut, Sparkline, StackedBars } from "@/components/charts"

describe("визуализации", () => {
  it("столбики показывают подписи и суммы", () => {
    render(
      <StackedBars
        data={[
          { label: "01.01", segments: [{ value: 2, className: "bg-emerald-500" }] },
          {
            label: "02.01",
            segments: [
              { value: 1, className: "bg-emerald-500" },
              { value: 1, className: "bg-destructive" },
            ],
          },
        ]}
      />,
    )

    expect(screen.getByText("01.01")).toBeInTheDocument()
    expect(screen.getByText("02.01")).toBeInTheDocument()
    expect(screen.getByTitle("02.01: 2")).toBeInTheDocument()
  })

  it("donut показывает значение в центре", () => {
    render(
      <Donut
        segments={[
          { label: "done", value: 3, className: "text-emerald-500" },
          { label: "failed", value: 1, className: "text-destructive" },
        ]}
        centerLabel="прогонов"
        centerValue="4"
      />,
    )

    expect(screen.getByText("4")).toBeInTheDocument()
    expect(screen.getByText("прогонов")).toBeInTheDocument()
  })

  it("спарклайн требует минимум две точки", () => {
    const { rerender } = render(<Sparkline values={[10]} />)
    expect(screen.getByText("Недостаточно данных")).toBeInTheDocument()

    rerender(<Sparkline values={[10, 20, 15]} />)
    expect(screen.getByRole("img", { name: "график латентности" })).toBeInTheDocument()
  })
})
