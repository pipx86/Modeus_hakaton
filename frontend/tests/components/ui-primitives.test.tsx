/**
 * Дымовые тесты shadcn-примитивов: проверяется, что компонент рендерится,
 * принимает className и пробрасывает атрибуты. Поведение Radix-обёрток
 * (Sheet, Tooltip, Tabs) здесь не тестируется - оно проверяется через страницы.
 */
import { render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"

describe("Button", () => {
  it("рендерит текст и вызывает onClick", () => {
    const onClick = vi.fn()
    render(<Button onClick={onClick}>Сохранить</Button>)

    const button = screen.getByRole("button", { name: "Сохранить" })
    button.click()

    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it("поддерживает вариант outline", () => {
    render(<Button variant="outline">Отмена</Button>)
    expect(screen.getByRole("button", { name: "Отмена" })).toBeInTheDocument()
  })

  it("блокируется атрибутом disabled", () => {
    const onClick = vi.fn()
    render(
      <Button disabled onClick={onClick}>
        Нельзя
      </Button>,
    )

    expect(screen.getByRole("button", { name: "Нельзя" })).toBeDisabled()
  })
})

describe("Badge", () => {
  it("рендерит подпись и вариант", () => {
    render(<Badge variant="secondary">Новый</Badge>)
    expect(screen.getByText("Новый")).toBeInTheDocument()
  })
})

describe("Card", () => {
  it("собирает заголовок, описание и содержимое", () => {
    render(
      <Card>
        <CardHeader>
          <CardTitle>Заголовок</CardTitle>
          <CardDescription>Описание</CardDescription>
        </CardHeader>
        <CardContent>Тело</CardContent>
      </Card>,
    )

    expect(screen.getByText("Заголовок")).toBeInTheDocument()
    expect(screen.getByText("Описание")).toBeInTheDocument()
    expect(screen.getByText("Тело")).toBeInTheDocument()
  })
})

describe("поля формы", () => {
  it("Input связывается с Label", () => {
    render(
      <>
        <Label htmlFor="name">Имя</Label>
        <Input id="name" defaultValue="Иван" />
      </>,
    )

    expect(screen.getByLabelText("Имя")).toHaveValue("Иван")
  })

  it("Textarea принимает ввод", () => {
    render(<Textarea aria-label="Комментарий" />)
    expect(screen.getByLabelText("Комментарий")).toBeInTheDocument()
  })

  it("Input пробрасывает placeholder", () => {
    render(<Input placeholder="Например: вакансия" />)
    expect(screen.getByPlaceholderText("Например: вакансия")).toBeInTheDocument()
  })
})

describe("Skeleton", () => {
  it("рендерит плейсхолдер с анимацией", () => {
    const { container } = render(<Skeleton className="h-4 w-10" />)
    expect(container.firstElementChild).toHaveClass("animate-pulse")
    expect(container.firstElementChild).toHaveClass("h-4")
  })
})
