import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { ActivityTree } from "@/components/activity-tree"
import { buildTree, NODE_TYPE_LABELS } from "@/components/activity-tree-model"
import type { ActivityNode, Evidence } from "@/lib/api/client"

const evidence = (id: string): Evidence => ({ id, source_id: "src-1", quote: `Цитата ${id}` })

const NODES: ActivityNode[] = [
  { id: "r1", type: "role", title: "Data Scientist", evidence: [] },
  { id: "f1", type: "function", title: "Анализ метрик", parent_id: "r1", evidence: [evidence("e1")] },
  { id: "f2", type: "function", title: "Коммуникации", parent_id: "r1", evidence: [] },
  { id: "t1", type: "task", title: "Когортные отчёты", parent_id: "f1", evidence: [evidence("e2")] },
  { id: "a1", type: "action", title: "Пишет SQL-запросы", parent_id: "t1", evidence: [evidence("e3")] },
]

function renderTree(overrides: Partial<Parameters<typeof ActivityTree>[0]> = {}) {
  return render(
    <ActivityTree
      nodes={NODES}
      approvedIds={new Set()}
      onApprove={vi.fn()}
      onEdit={vi.fn()}
      onAttach={vi.fn()}
      {...overrides}
    />,
  )
}

describe("buildTree", () => {
  it("собирает иерархию по parent_id", () => {
    const roots = buildTree(NODES)

    expect(roots).toHaveLength(1)
    expect(roots[0].id).toBe("r1")
    expect(roots[0].children.map((node) => node.id)).toEqual(["f1", "f2"])
    expect(roots[0].children[0].children[0].id).toBe("t1")
  })

  it("узел с несуществующим родителем становится корнем", () => {
    const roots = buildTree([
      { id: "x", type: "role", title: "Сирота", parent_id: "nope", evidence: [] },
    ])

    expect(roots.map((node) => node.id)).toEqual(["x"])
  })

  it("на пустом списке возвращает пустой лес", () => {
    expect(buildTree([])).toEqual([])
  })
})

describe("ActivityTree", () => {
  it("строит доступные имена типов узлов", () => {
    expect(NODE_TYPE_LABELS).toEqual({
      role: "Роль",
      function: "Функция",
      task: "Задача",
      action: "Действие",
    })
  })

  it("свёрнутая ветка скрывает потомков, шеврон раскрывает и сворачивает", () => {
    renderTree()

    expect(screen.getByText("Анализ метрик")).toBeInTheDocument()
    expect(screen.queryByText("Когортные отчёты")).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Развернуть узел Анализ метрик" }))
    expect(screen.getByText("Когортные отчёты")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Свернуть узел Анализ метрик" }))
    expect(screen.queryByText("Когортные отчёты")).not.toBeInTheDocument()
  })

  it("«развернуть всё» и «свернуть всё» управляют всеми ветками", () => {
    renderTree()

    fireEvent.click(screen.getByRole("button", { name: "Развернуть всё" }))
    expect(screen.getByText("Пишет SQL-запросы")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Свернуть всё" }))
    expect(screen.queryByText("Пишет SQL-запросы")).not.toBeInTheDocument()
  })

  it("поиск оставляет совпадение, его предков и поддерево", () => {
    renderTree()

    fireEvent.change(screen.getByLabelText("Поиск по узлам дерева"), {
      target: { value: "когорт" },
    })

    expect(
      screen.getByRole("button", { name: "Узел Когортные отчёты: показать обоснования" }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole("button", { name: "Узел Пишет SQL-запросы: показать обоснования" }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole("button", { name: "Узел Коммуникации: показать обоснования" }),
    ).not.toBeInTheDocument()
  })

  it("фильтр по типу скрывает узлы вместе с поддеревом", () => {
    renderTree()
    fireEvent.click(screen.getByRole("button", { name: "Развернуть всё" }))

    fireEvent.click(screen.getByRole("button", { name: "Задача", pressed: true }))

    expect(screen.queryByText("Когортные отчёты")).not.toBeInTheDocument()
    expect(screen.queryByText("Пишет SQL-запросы")).not.toBeInTheDocument()
    expect(screen.getByText("Анализ метрик")).toBeInTheDocument()
  })

  it("фильтр «с обоснованиями» скрывает узлы без цитат", () => {
    renderTree()

    fireEvent.click(screen.getByRole("button", { name: "С обоснованиями", pressed: false }))

    expect(screen.queryByText("Коммуникации")).not.toBeInTheDocument()
    expect(screen.getByText("Анализ метрик")).toBeInTheDocument()
  })

  it("клик по узлу открывает диалог, подтверждение зовёт onApprove с id цитаты", () => {
    const onApprove = vi.fn()
    renderTree({ onApprove })

    fireEvent.click(screen.getByRole("button", { name: "Узел Анализ метрик: показать обоснования" }))
    expect(screen.getByText("Цитата e1")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Подтвердить цитату" }))
    expect(onApprove).toHaveBeenCalledWith("e1")
  })

  it("пустой результат поиска показывает сброс фильтров", () => {
    renderTree()

    fireEvent.change(screen.getByLabelText("Поиск по узлам дерева"), { target: { value: "zzz" } })
    expect(screen.getByText("Ничего не найдено")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Сбросить фильтры" }))
    expect(screen.getByText("Data Scientist")).toBeInTheDocument()
  })

  it("свёрнутое дерево из 60+ узлов рендерит только раскрытый frontier", () => {
    const big: ActivityNode[] = [
      { id: "root", type: "role", title: "Роль", evidence: [] },
      { id: "fn", type: "function", title: "Функция", parent_id: "root", evidence: [] },
    ]
    for (let i = 1; i <= 60; i += 1) {
      big.push({ id: `act-${i}`, type: "action", title: `Операция ${i}`, parent_id: "fn", evidence: [] })
    }

    renderTree({ nodes: big })
    expect(screen.getAllByRole("button", { name: /показать обоснования/ })).toHaveLength(2)

    fireEvent.click(screen.getByRole("button", { name: "Развернуть всё" }))
    expect(screen.getAllByRole("button", { name: /показать обоснования/ })).toHaveLength(62)
  })

  it("уже подтверждённая цитата показывает статус вместо кнопки", () => {
    renderTree({ approvedIds: new Set(["e1"]) })

    fireEvent.click(screen.getByRole("button", { name: "Узел Анализ метрик: показать обоснования" }))

    expect(screen.getByText("Подтверждено")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Подтвердить цитату" })).not.toBeInTheDocument()
  })
})
