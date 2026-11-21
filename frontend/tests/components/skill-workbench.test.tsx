import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { setFetchHandler, fetchCalls } from "@/test/fetch-stub";
import { SkillWorkbench } from "@/components/skill-workbench";
const model = { revision: 0, tools: [{ id: "python", name: "Python", kind: "language", aliases: ["Питон"] }], definitions: [], links: [] };
function stub(conflict = false) { setFetchHandler(async (url, init) => { if (url.endsWith('/nodes'))
    return { status: 200, body: [] }; if (init.method === 'PUT') {
    if (conflict)
        return { status: 409, body: { message: "Словарь изменён другим пользователем" } };
    return { status: 200, body: { ...JSON.parse(String(init.body)), revision: 1 } };
} return { status: 200, body: model }; }); }
describe("SkillWorkbench", () => {
    it("creates a structured definition and saves explicitly", async () => { stub(); render(<SkillWorkbench runId="run"/>); await screen.findByText("Версия словаря: 0"); fireEvent.change(screen.getAllByLabelText("Название", { selector: 'input', exact: true })[1], { target: { value: "Объясняет транзакции" } }); fireEvent.click(screen.getByRole("button", { name: "Добавить определение" })); expect(fetchCalls().some(c => c.init.method === 'PUT')).toBe(false); expect(screen.getByText(/Нужно уточнить:/)).toBeInTheDocument(); fireEvent.click(screen.getByRole("button", { name: "Сохранить словарь" })); await screen.findByText("Версия словаря: 1"); const call = fetchCalls().find(c => c.init.method === 'PUT'); expect(JSON.parse(String(call?.init.body)).definitions[0]).toMatchObject({ title: "Объясняет транзакции", kind: "knowledge", conditions: "", version: 1 }); });
    it("preserves the draft on concurrent update conflict", async () => { stub(true); render(<SkillWorkbench runId="run"/>); await screen.findByText("Версия словаря: 0"); fireEvent.change(screen.getAllByLabelText("Название", { selector: 'input', exact: true })[1], { target: { value: "Черновик" } }); fireEvent.click(screen.getByRole("button", { name: "Добавить определение" })); fireEvent.click(screen.getByRole("button", { name: "Сохранить словарь" })); await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("изменён другим пользователем")); expect(screen.getByText("Черновик")).toBeInTheDocument(); expect(screen.getByRole("button", { name: "Сохранить словарь" })).toBeEnabled(); });
    it("keeps read only viewers from modifying or extracting", async () => { stub(); render(<SkillWorkbench runId="run" readOnly/>); await screen.findByText("Версия словаря: 0"); expect(screen.queryByText("Сохранить словарь")).not.toBeInTheDocument(); expect(screen.queryByText("Извлечь предложения из дерева")).not.toBeInTheDocument(); });
});
