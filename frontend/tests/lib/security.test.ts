import { describe, expect, it } from "vitest"
import { safeHttpUrl, stripControlChars } from "@/lib/security"

describe("safeHttpUrl", () => {
  it("пропускает http и https", () => {
    expect(safeHttpUrl("https://example.com/vacancy/1")).toBe("https://example.com/vacancy/1")
    expect(safeHttpUrl("http://example.com")).toBe("http://example.com/")
  })

  it("отклоняет javascript:", () => {
    expect(safeHttpUrl("javascript:alert(1)")).toBeNull()
    expect(safeHttpUrl("JavaScript:alert(1)")).toBeNull()
    expect(safeHttpUrl("  javascript:alert(1)  ")).toBeNull()
  })

  it("отклоняет data: и vbscript:", () => {
    expect(safeHttpUrl("data:text/html,<script>alert(1)</script>")).toBeNull()
    expect(safeHttpUrl("vbscript:msgbox(1)")).toBeNull()
  })

  it("отклоняет пустые значения и мусор", () => {
    expect(safeHttpUrl("")).toBeNull()
    expect(safeHttpUrl("   ")).toBeNull()
    expect(safeHttpUrl(undefined)).toBeNull()
    expect(safeHttpUrl(null)).toBeNull()
    expect(safeHttpUrl("не ссылка")).toBeNull()
  })
})

describe("stripControlChars", () => {
  it("удаляет управляющие символы", () => {
    expect(stripControlChars("привет\u0000мир\u0007")).toBe("приветмир")
    expect(stripControlChars("строка\u001Fс\u007Fконцом")).toBe("строкасконцом")
  })

  it("сохраняет переводы строк и табы", () => {
    expect(stripControlChars("первая\nвторая\tтретья")).toBe("первая\nвторая\tтретья")
  })

  it("не трогает обычный текст и разметку", () => {
    expect(stripControlChars("<img src=x onerror=alert(1)>")).toBe("<img src=x onerror=alert(1)>")
  })
})
