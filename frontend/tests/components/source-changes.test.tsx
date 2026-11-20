import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { SourceChanges } from "@/components/source-changes"
import { setFetchHandler } from "@/test/fetch-stub"

describe("SourceChanges", () => {
  it("показывает затронутый узел и открывает его", async () => {
    setFetchHandler(async () => ({ status: 200, body: [{
      source_id: "s1", source_title: "Руководство", node_id: "n1", node_title: "Создать запрос",
      evidence_id: "e1", changed_at: "2026-11-21T12:00:00Z",
    }] }))
    const focus = vi.fn()
    render(<SourceChanges runId="r1" onFocusNode={focus} />)
    expect(await screen.findByText(/Руководство/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: "Создать запрос" }))
    expect(focus).toHaveBeenCalledWith("n1")
    expect(screen.getByText(/Учитываются изменения после включения/)).toBeInTheDocument()
  })
})
