import { render, screen } from "@testing-library/react"
import { MemoryRouter } from "react-router-dom"
import { describe, expect, it } from "vitest"
import { NotFound } from "@/pages/not-found"

describe("NotFound", () => {
  it("показывает понятное сообщение и ссылку на главную", () => {
    render(
      <MemoryRouter>
        <NotFound />
      </MemoryRouter>,
    )

    expect(screen.getByText("Страница не найдена")).toBeInTheDocument()
    expect(screen.getByRole("link", { name: "К списку исследований" })).toHaveAttribute("href", "/")
  })
})
