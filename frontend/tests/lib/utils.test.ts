import { describe, expect, it } from "vitest"
import { cn } from "@/lib/utils"

describe("cn", () => {
  it("склеивает классы через пробел", () => {
    expect(cn("a", "b")).toBe("a b")
  })

  it("отбрасывает false, undefined, null и пустые строки", () => {
    expect(cn("a", false, undefined, null, "")).toBe("a")
  })

  it("поддерживает объектную форму clsx", () => {
    expect(cn({ a: true, b: false })).toBe("a")
  })

  it("снимает конфликт tailwind в пользу последнего класса", () => {
    expect(cn("px-2", "px-4")).toBe("px-4")
    expect(cn("text-sm", "text-lg")).toBe("text-lg")
  })

  it("не дублирует одинаковые классы", () => {
    expect(cn("p-2", "p-2")).toBe("p-2")
  })
})
