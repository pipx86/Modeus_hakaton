/**
 * @file export-chat.ts
 * @description Экспорт истории интервью в Markdown для просмотра и печати.
 */

import type { ChatMessage } from "@/lib/api/client"

/** Собирает транскрипт интервью: реплики эксперта и агента плюс цитаты источников. */
export function chatTranscriptToMarkdown(messages: ChatMessage[], title: string): string {
  const lines: string[] = [`# ${title}`, ""]
  for (const message of messages) {
    const author = message.role === "user" ? "Эксперт" : "Агент"
    lines.push(`**${author}:** ${message.content}`, "")
    if (message.citations && message.citations.length > 0) {
      const citations = message.citations
        .map((citation) => `${citation.title}: «${citation.quote}»`)
        .join("; ")
      lines.push(`> Источники: ${citations}`, "")
    }
  }
  return `${lines.join("\n").trimEnd()}\n`
}
