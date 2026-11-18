import { describe, expect, it } from "vitest"
import { chatTranscriptToMarkdown } from "@/lib/export-chat"
import type { ChatMessage } from "@/lib/api/client"

const messages: ChatMessage[] = [
  { id: "m1", role: "agent", content: "Какие задачи вы решаете?", created_at: "2026-11-10T10:00:00Z" },
  { id: "m2", role: "user", content: "Строю отчёты", created_at: "2026-11-10T10:01:00Z" },
  {
    id: "m3",
    role: "agent",
    content: "Уточните формат",
    created_at: "2026-11-10T10:02:00Z",
    citations: [{ title: "Вакансия DS", quote: "SQL и дашборды" }],
  },
]

describe("chatTranscriptToMarkdown", () => {
  it("сохраняет роли, текст и цитаты источников", () => {
    const markdown = chatTranscriptToMarkdown(messages, "Интервью")

    expect(markdown).toContain("# Интервью")
    expect(markdown).toContain("**Агент:** Какие задачи вы решаете?")
    expect(markdown).toContain("**Эксперт:** Строю отчёты")
    expect(markdown).toContain("> Источники: Вакансия DS: «SQL и дашборды»")
  })

  it("возвращает заголовок даже для пустого чата", () => {
    expect(chatTranscriptToMarkdown([], "Интервью")).toContain("# Интервью")
  })
})
