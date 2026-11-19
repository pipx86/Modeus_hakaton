import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { expect, it, vi } from "vitest"

import { fetchCalls, setFetchHandler } from "@/test/fetch-stub"
import { ActivityTree } from "@/components/activity-tree"
import { ChatTab } from "@/components/ChatTab"

const exactText = "  О'Нил: \"цитата\" <mark data-proof='literal'>& 3 < 7 \\ путь\n  "

class LocalEventSource {
  addEventListener() {}
  close() {}
}

it("отправляет исходный ответ и отображает его текстом", async () => {
  vi.stubGlobal("EventSource", LocalEventSource)
  setFetchHandler(async (_url, init) => {
    if (init.method === "POST") {
      const sent = JSON.parse(String(init.body)) as { content: string }
      return {
        status: 201,
        body: { id: "reply", role: "user", content: sent.content, created_at: "2026-11-18T10:00:00Z" },
      }
    }
    return {
      status: 200,
      body: [{ id: "prompt", role: "agent", content: "Ваш ответ?", created_at: "2026-11-18T09:00:00Z" }],
    }
  })
  const { container } = render(<ChatTab active runId="literal" />)
  await screen.findByText("Ваш ответ?")
  fireEvent.change(screen.getByRole("textbox"), { target: { value: exactText } })
  fireEvent.click(screen.getByRole("button", { name: "Отправить" }))
  await waitFor(() => {
    const sent = fetchCalls().find((call) => call.init.method === "POST")
    expect(JSON.parse(String(sent?.init.body))).toEqual({ content: exactText })
    expect([...container.querySelectorAll("p")].some((node) => node.textContent === exactText)).toBe(true)
  })
  expect(container.querySelector("mark[data-proof]")).toBeNull()
})

it("показывает цитату в обоснованиях дерева как текст", () => {
  render(<ActivityTree
    nodes={[{ id: "role", title: "Техник", type: "role", evidence: [{ id: "quote", source_id: "src", quote: exactText }] }]}
    approvedIds={new Set()}
    onApprove={vi.fn()}
    onEdit={vi.fn()}
    onAttach={vi.fn()}
  />)
  fireEvent.click(screen.getByRole("button", { name: "Узел Техник: показать обоснования" }))
  const dialog = screen.getByRole("dialog")
  expect([...dialog.querySelectorAll("span")].some((node) => node.textContent === exactText)).toBe(true)
  expect(dialog.querySelector("mark[data-proof]")).toBeNull()
})
