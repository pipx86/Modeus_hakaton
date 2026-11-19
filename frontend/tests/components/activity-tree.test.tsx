import { fireEvent, render, screen, within } from "@testing-library/react"
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

  it("диалог обоснований показывает статус и confidence и умеет отклонить", () => {
    const onReject = vi.fn()
    renderTree({
      nodes: [
        { id: "r1", type: "role", title: "Data Scientist", evidence: [] },
        {
          id: "f1",
          type: "function",
          title: "Анализ метрик",
          parent_id: "r1",
          evidence: [
            { id: "e1", source_id: "s-1", quote: "Цитата e1", status: "pending", confidence: 0.82 } as Evidence & {
              confidence?: number
            },
          ],
        },
      ],
      onReject,
    })

    fireEvent.click(
      screen.getByRole("button", { name: "Узел Анализ метрик: показать обоснования" }),
    )

    expect(screen.getByText("На проверке")).toBeInTheDocument()
    expect(screen.getByText("82%")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Отклонить цитату" }))
    expect(onReject).toHaveBeenCalledWith("e1")
  })

  it("XSS-payload в названии узла рендерится текстом, а не разметкой", () => {
    renderTree({
      nodes: [{ id: "r1", type: "role", title: '<img src=x onerror="alert(1)">', evidence: [] }],
    })

    expect(screen.getByText(/onerror="alert\(1\)"/)).toBeInTheDocument()
    expect(document.querySelector("img")).toBeNull()
  })

  it("пустой результат поиска показывает сброс фильтров", () => {
    renderTree()

    fireEvent.change(screen.getByLabelText("Поиск по узлам дерева"), { target: { value: "zzz" } })
    expect(screen.getByText("Ничего не найдено")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Сбросить фильтры" }))
    expect(screen.getByText("Data Scientist")).toBeInTheDocument()
  })

  it("раскрывает ветку к узлу из ссылки", () => {
    renderTree({
      nodes: [
        { id: "root", type: "role", title: "Роль", evidence: [] },
        { id: "fn", type: "function", title: "Функция", parent_id: "root", evidence: [] },
        { id: "act", type: "action", title: "Глубокое действие", parent_id: "fn", evidence: [] },
      ],
      focusNodeId: "act",
    })

    expect(screen.getByText("Глубокое действие")).toBeInTheDocument()
  })

  it("перемещает фокус стрелкой вниз", () => {
    renderTree()
    const first = screen.getByRole("button", { name: /Узел Data Scientist/ })
    first.focus()

    fireEvent.keyDown(first, { key: "ArrowDown" })

    expect(document.activeElement?.getAttribute("aria-label")).toContain("Анализ метрик")
  })

  it("большое дерево рендерится окнами, а не целиком", () => {
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

    expect(document.querySelector('[data-virtualized="true"]')).not.toBeNull()
    const rendered = document.querySelectorAll("[data-row]")
    expect(rendered.length).toBeGreaterThan(0)
    expect(rendered.length).toBeLessThan(62)
    expect(screen.getByText("Операция 1")).toBeInTheDocument()
    expect(screen.queryByText("Операция 60")).not.toBeInTheDocument()
  })

  it("показывает ЗУН действия и раскрывает списки", () => {
    renderTree({
      nodes: [
        { id: "root", type: "role", title: "Роль", evidence: [] },
        { id: "fn", type: "function", title: "Функция", parent_id: "root", evidence: [] },
        {
          id: "act",
          type: "action",
          title: "Действие с ЗУН",
          parent_id: "fn",
          evidence: [],
          zun: {
            knowledge: ["Устройство когортного анализа"],
            skills: ["Написание SQL-запросов"],
            abilities: ["Точность формулировок"],
          },
        },
      ],
    })
    fireEvent.click(screen.getByRole("button", { name: "Развернуть всё" }))

    fireEvent.click(screen.getByRole("button", { name: "ЗУН узла Действие с ЗУН" }))

    expect(screen.getByText("Знания")).toBeInTheDocument()
    expect(screen.getByText("Устройство когортного анализа")).toBeInTheDocument()
    expect(screen.getByText("Написание SQL-запросов")).toBeInTheDocument()
    expect(screen.getByText("Точность формулировок")).toBeInTheDocument()
  })

  it("фильтр «С ЗУН» оставляет только действия с ЗУН", () => {
    renderTree({
      nodes: [
        { id: "root", type: "role", title: "Роль", evidence: [] },
        { id: "fn", type: "function", title: "Функция", parent_id: "root", evidence: [] },
        {
          id: "a1",
          type: "action",
          title: "Действие с ЗУН",
          parent_id: "fn",
          evidence: [],
          zun: { knowledge: ["К"], skills: [], abilities: [] },
        },
        { id: "a2", type: "action", title: "Действие без ЗУН", parent_id: "fn", evidence: [] },
      ],
    })

    fireEvent.click(screen.getByRole("button", { name: "С ЗУН" }))

    expect(screen.getByText("Действие с ЗУН")).toBeInTheDocument()
    expect(screen.queryByText("Действие без ЗУН")).not.toBeInTheDocument()
  })

  it("уже подтверждённая цитата показывает статус вместо кнопки", () => {
    renderTree({ approvedIds: new Set(["e1"]) })

    fireEvent.click(screen.getByRole("button", { name: "Узел Анализ метрик: показать обоснования" }))

    expect(screen.getByText("Подтверждено")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Подтвердить цитату" })).not.toBeInTheDocument()
  })

  it("копирует паспорт компетенции из диалога ЗУН", () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true })
    renderTree({
      nodes: [
        { id: "root", type: "role", title: "Роль", evidence: [] },
        {
          id: "act",
          type: "action",
          title: "Действие с ЗУН",
          parent_id: "root",
          evidence: [{ id: "e1", source_id: "s1", quote: "Цитата действия" }],
          zun: {
            knowledge: ["Знание"],
            skills: ["Умение"],
            abilities: ["Навык"],
          },
        },
      ],
    })
    fireEvent.click(screen.getByRole("button", { name: "Развернуть всё" }))
    fireEvent.click(screen.getByRole("button", { name: "ЗУН узла Действие с ЗУН" }))
    fireEvent.click(screen.getByRole("button", { name: "Скопировать паспорт" }))

    expect(writeText).toHaveBeenCalledTimes(1)
    const passport = String(writeText.mock.calls[0][0])
    expect(passport).toContain("Действие с ЗУН")
    expect(passport).toContain("Знания:")
  })

  it("копирует ссылку на узел дерева", () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true })
    renderTree()

    fireEvent.click(
      screen.getByRole("button", { name: "Узел Анализ метрик: показать обоснования" }),
    )
    fireEvent.click(screen.getByRole("button", { name: "Скопировать ссылку" }))

    expect(writeText).toHaveBeenCalledTimes(1)
    expect(String(writeText.mock.calls[0][0])).toContain("?node=f1")
  })

  it("показывает тип, приоритет и объяснение узла", () => {
    const sources = new Map([
      ["src-1", { id: "src-1", type: "interview" as const, title: "Интервью с аналитиком" }],
    ])
    renderTree({ sourcesById: sources })

    fireEvent.click(
      screen.getByRole("button", { name: "Узел Анализ метрик: показать обоснования" }),
    )

    const dialog = screen.getByRole("dialog")
    expect(within(dialog).getByText(/подтверждён/)).toBeInTheDocument()
    expect(within(dialog).getByText("Интервью")).toBeInTheDocument()
    expect(within(dialog).getByText("· 300")).toBeInTheDocument()
  })

  it("фильтр «Проблемные» оставляет узлы с замечаниями", () => {
    renderTree({
      nodes: [
        {
          id: "r1",
          type: "role",
          title: "Аналитик",
          evidence: [evidence("e1")],
        },
        { id: "f1", type: "function", title: "Анализ", parent_id: "r1", evidence: [] },
        {
          id: "a1",
          type: "action",
          title: "Проверять отчёты",
          parent_id: "r1",
          evidence: [evidence("e2")],
        },
      ],
    })

    fireEvent.click(screen.getByRole("button", { name: /Проблемные/ }))

    expect(screen.getByText("Анализ")).toBeInTheDocument()
    expect(screen.queryByText("Проверять отчёты")).not.toBeInTheDocument()
  })

  it("использует серверные insights: здоровье, флаги и рынок", () => {
    const insights = {
      run_id: "7",
      provenance: {
        nodes: {
          f1: { source_count: 2, source_types: ["interview", "standard"], evidence_count: 2 },
        },
      },
      health: {
        score: 82,
        breakdown: { groundedness: 90, completeness: 80, duplicates: 100, orphans: 70 },
        weights: { groundedness: 0.4, completeness: 0.3, duplicates: 0.2, orphans: 0.1 },
        counts: {
          node_count: 1,
          grounded_nodes: 1,
          duplicate_nodes: 0,
          orphan_nodes: 0,
          childless_nodes: 0,
        },
        formula: "0.4*groundedness + ...",
      },
      flags: [{ node_id: "f1", codes: ["NO_EVIDENCE"], reasons: ["Нет цитат"] }],
      market_heat: {
        source: "run.skill_freq",
        top: [{ skill: "sql", frequency: 2 }],
        nodes: { f1: { score: 2, matches: ["sql"] } },
      },
      coverage: { columns: [], rows: [] },
      sources: [],
      node_explanations: {
        f1: { summary: "Серверное объяснение узла", evidence: [] },
      },
    }
    renderTree({ insights: insights as never })

    expect(screen.getByText("Здоровье: 82")).toBeInTheDocument()
    expect(screen.getByText("Проблемные")).toBeInTheDocument()
    expect(screen.getByText("рынок ×2")).toBeInTheDocument()
    expect(screen.getByText("sql · 2")).toBeInTheDocument()
    expect(screen.getByText("2 типа")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Здоровье дерева: 82 из 100" }))
    expect(screen.getByText("Полнота")).toBeInTheDocument()
  })

  it("в режиме просмотра скрывает изменяющие действия", () => {
    renderTree({ readOnly: true })

    expect(screen.queryByRole("button", { name: /Редактировать узел/ })).not.toBeInTheDocument()
    expect(
      screen.queryByRole("button", { name: /Добавить обоснование к узлу/ }),
    ).not.toBeInTheDocument()

    fireEvent.click(
      screen.getByRole("button", { name: "Узел Анализ метрик: показать обоснования" }),
    )
    expect(screen.queryByRole("button", { name: "Подтвердить цитату" })).not.toBeInTheDocument()
  })

  it("переходит к цитате в интервью", () => {    const onShowTranscript = vi.fn()
    const sources = new Map([
      ["src-1", { id: "src-1", type: "interview" as const, title: "Интервью" }],
    ])
    renderTree({
      nodes: [
        {
          id: "f1",
          type: "function",
          title: "Анализ метрик",
          evidence: [{ id: "e1", source_id: "src-1", ref_id: "msg-1", quote: "Цитата" }],
        },
      ],
      sourcesById: sources,
      onShowTranscript,
    })

    fireEvent.click(
      screen.getByRole("button", { name: "Узел Анализ метрик: показать обоснования" }),
    )
    fireEvent.click(screen.getByRole("button", { name: "В интервью" }))

    expect(onShowTranscript).toHaveBeenCalledWith(
      expect.objectContaining({ ref_id: "msg-1" }),
      expect.objectContaining({ type: "interview" }),
    )
  })
})
