/**
 * @file activity-tree.test.tsx
 * @description Тесты экрана дерева без сети и бэкенда: проверяются раскрытие/сворачивание,
 * «развернуть/свернуть всё», поиск с сохранением предков и поддерева совпадения, фильтры
 * по типу и «с обоснованиями», диалог обоснований по клику с подтверждением, пустой
 * результат фильтров и ленивый рендер на дереве из 60+ узлов. Колбэки мутаций — `vi.fn()`,
 * чтобы тест фиксировал факт вызова с правильным id, не трогая сеть.
 *
 * Совпавшие с поиском узлы проверяются через `getByRole` по `aria-label`, а не через
 * `getByText`: компонент подсветки дробит заголовок совпадения на текстовые ноды и `<mark>`,
 * из-за чего точный `getByText` по такому узлу не находит элемент (`getNodeText` берёт только
 * прямые текстовые дети). Доступное имя из `aria-label` не дробится и остаётся устойчивым.
 */
/// <reference types="@testing-library/jest-dom" />

import { fireEvent, render, screen } from "@testing-library/react"
import { expect, test, vi } from "vitest"
import { ActivityTree } from "./activity-tree"
import type { ActivityNode, Evidence } from "@/lib/api/client"

/** Строит цитату по id; `source_id` фиктивен — в этих тестах источник не загружается. */
const evidence = (id: string): Evidence => ({ id, source_id: "src-1", quote: `Цитата ${id}` })

const NODES: ActivityNode[] = [
  { id: "r1", type: "role", title: "Data Scientist", evidence: [] },
  { id: "f1", type: "function", title: "Анализ метрик", parent_id: "r1", evidence: [evidence("e1")] },
  { id: "f2", type: "function", title: "Коммуникации", parent_id: "r1", evidence: [] },
  { id: "t1", type: "task", title: "Когортные отчёты", parent_id: "f1", evidence: [evidence("e2")] },
  { id: "a1", type: "action", title: "Пишет SQL-запросы", parent_id: "t1", evidence: [evidence("e3")] },
]

/** Монтирует дерево с дефолтными заглушками колбэков; переопределяет пропсы через `overrides`. */
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

test("свёрнутая ветка скрывает потомков, шеврон раскрывает и сворачивает", () => {
  renderTree()
  expect(screen.getByText("Анализ метрик")).toBeInTheDocument()
  expect(screen.queryByText("Когортные отчёты")).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole("button", { name: "Развернуть узел Анализ метрик" }))
  expect(screen.getByText("Когортные отчёты")).toBeInTheDocument()
  fireEvent.click(screen.getByRole("button", { name: "Свернуть узел Анализ метрик" }))
  expect(screen.queryByText("Когортные отчёты")).not.toBeInTheDocument()
})

test("кнопки «развернуть всё» и «свернуть всё» управляют всеми ветками", () => {
  renderTree()
  fireEvent.click(screen.getByRole("button", { name: "Развернуть всё" }))
  expect(screen.getByText("Пишет SQL-запросы")).toBeInTheDocument()
  fireEvent.click(screen.getByRole("button", { name: "Свернуть всё" }))
  expect(screen.queryByText("Пишет SQL-запросы")).not.toBeInTheDocument()
})

test("поиск оставляет совпадение с предками и поддеревом, скрывает остальное", () => {
  renderTree()
  fireEvent.change(screen.getByLabelText("Поиск по узлам дерева"), {
    target: { value: "когорт" },
  })
  // Совпавший узел (t1) и его потомок (a1) ищем по доступному имени кнопки, а не по
  // голому тексту: подсветка дробит заголовок t1 на «К» + <mark>огорт</mark> + «ные отчёты»,
  // и точный getByText по такому узлу не сработал бы. Предки (r1, f1) смонтированы как
  // контейнеры пути; скрытый фильтром f2 не смонтирован вовсе.
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

test("фильтр по типу выключает узлы типа вместе с поддеревом", () => {
  renderTree()
  fireEvent.click(screen.getByRole("button", { name: "Развернуть всё" }))
  fireEvent.click(screen.getByRole("button", { name: "Задача", pressed: true }))
  expect(screen.queryByText("Когортные отчёты")).not.toBeInTheDocument()
  expect(screen.queryByText("Пишет SQL-запросы")).not.toBeInTheDocument()
  expect(screen.getByText("Анализ метрик")).toBeInTheDocument()
})

test("фильтр «с обоснованиями» скрывает узлы без цитат", () => {
  renderTree()
  fireEvent.click(screen.getByRole("button", { name: "С обоснованиями", pressed: false }))
  expect(screen.queryByText("Коммуникации")).not.toBeInTheDocument()
  expect(screen.getByText("Анализ метрик")).toBeInTheDocument()
})

test("клик по узлу открывает диалог с обоснованиями и подтверждением", () => {
  const onApprove = vi.fn()
  renderTree({ onApprove })
  fireEvent.click(screen.getByRole("button", { name: "Узел Анализ метрик: показать обоснования" }))
  expect(screen.getByText("Цитата e1")).toBeInTheDocument()
  fireEvent.click(screen.getByRole("button", { name: "Подтвердить цитату" }))
  expect(onApprove).toHaveBeenCalledWith("e1")
})

test("пустой результат поиска показывает состояние «ничего не найдено» и сброс", () => {
  renderTree()
  fireEvent.change(screen.getByLabelText("Поиск по узлам дерева"), { target: { value: "zzz" } })
  expect(screen.getByText("Ничего не найдено")).toBeInTheDocument()
  fireEvent.click(screen.getByRole("button", { name: "Сбросить фильтры" }))
  expect(screen.getByText("Data Scientist")).toBeInTheDocument()
})

test("свёрнутое дерево из 60+ узлов рендерует только раскрытый frontier (ленивые ветки)", () => {
  // Корень → одна функция → 60 действий. На старте раскрыт только корень, поэтому в DOM
  // две строки (корень и функция); 60 действий свёрнуты внутри функции и не смонтированы.
  const big: ActivityNode[] = [
    { id: "root", type: "role", title: "Роль", evidence: [] },
    { id: "fn", type: "function", title: "Функция", parent_id: "root", evidence: [] },
  ]
  for (let i = 1; i <= 60; i += 1) {
    big.push({
      id: `act-${i}`,
      type: "action",
      title: `Операция ${i}`,
      parent_id: "fn",
      evidence: [],
    })
  }
  renderTree({ nodes: big })
  expect(screen.getAllByRole("button", { name: /показать обоснования/ })).toHaveLength(2)
  fireEvent.click(screen.getByRole("button", { name: "Развернуть всё" }))
  expect(screen.getAllByRole("button", { name: /показать обоснования/ })).toHaveLength(62)
})