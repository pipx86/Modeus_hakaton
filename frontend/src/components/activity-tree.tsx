/**
 * @file activity-tree.tsx
 * @description Интерактивное представление дерева деятельности запуска: панель поиска и
 * фильтров, раскрытие/сворачивание веток, ленивый рендер свёрнутых поддеревьев и диалог
 * обоснований по клику на узел. Компонент не ходит в сеть и не владеет мутациями -
 * загрузку дерева, подтверждение цитат и диалоги редактирования/прикрепления держит
 * страница Research; сюда они передаются колбэками и множествами состояний.
 *
 * Экспорты: {@link ActivityTree} (основной компонент), {@link buildTree} (сборка
 * иерархии из плоского списка контракта), {@link NODE_TYPE_LABELS} (подписи типов узлов),
 * тип {@link TreeNode}.
 */
import { memo, useCallback, useEffect, useMemo, useState } from "react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import {
  CheckIcon,
  ChevronRightIcon,
  ChevronsDownUpIcon,
  ChevronsUpDownIcon,
  PencilIcon,
  PlusIcon,
  QuoteIcon,
  SearchIcon,
  SearchXIcon,
} from "lucide-react"
import type { ActivityNode, Evidence, NodeType } from "@/lib/api/client"

/**
 * Узел дерева с восстановленными детьми. Контракт отдаёт плоский список {@link ActivityNode},
 * где иерархия выражена полем `parent_id`; эта типа-обёртка добавляет массив `children`,
 * собранный {@link buildTree}.
 */
export type TreeNode = ActivityNode & { children: TreeNode[] }

/**
 * Человеческие подписи типов узлов. Экспортируется, чтобы страница Research использовала
 * те же строки в диалоге редактирования (select типа) без дублирования литералов.
 */
export const NODE_TYPE_LABELS: Record<NodeType, string> = {
  role: "Роль",
  function: "Функция",
  task: "Задача",
  action: "Действие",
}

/** Порядок типов для чипов-фильтров: от общего к частному, как в самой методологии дерева. */
const TYPE_ORDER: NodeType[] = ["role", "function", "task", "action"]

/**
 * Собирает иерархию из плоского списка контракта.
 *
 * Алгоритм двухпроходный: сначала индексируем все узлы в `Map` id -> {@link TreeNode}
 * с пустым `children`, затем для каждого узла находим родителя по `parent_id` и
 * прицепляем в его `children`; узлы без родителя (или с отсутствующим родителем)
 * становятся корнями. Отсутствие второго обхода дерева делает сборку O(n).
 *
 * @param nodes Плоский массив узлов из `GET /runs/{id}/tree`.
 * @return Корни леса; каждый корень несёт рекурсивно собранных потомков в `children`.
 */
export function buildTree(nodes: ActivityNode[]): TreeNode[] {
  const map = new Map<string, TreeNode>()
  for (const node of nodes) map.set(node.id, { ...node, children: [] })

  const roots: TreeNode[] = []
  for (const node of nodes) {
    const current = map.get(node.id)
    if (!current) continue
    const parent = node.parent_id ? map.get(node.parent_id) : undefined
    if (parent) parent.children.push(current)
    else roots.push(current)
  }
  return roots
}

/**
 * Заголовок узла с подсветкой совпадения поиска. Если запрос пуст или не найден -
 * возвращает текст без обёртки, чтобы не плодить лишние DOM-узлы на каждый рендер.
 *
 * @param title Полный заголовок узла.
 * @param query Текущий поисковый запрос (может быть пустым).
 */
function HighlightedTitle({ title, query }: { title: string; query: string }) {
  const q = query.trim()
  if (!q) return <>{title}</>
  const index = title.toLowerCase().indexOf(q.toLowerCase())
  if (index === -1) return <>{title}</>
  return (
    <>
      {title.slice(0, index)}
      <mark className="rounded bg-primary/20 px-0.5 text-inherit">
        {title.slice(index, index + q.length)}
      </mark>
      {title.slice(index + q.length)}
    </>
  )
}

/**
 * Список обоснований узла с кнопкой подтверждения для ещё не одобренных цитат.
 * Пустой список рендерит `null` - диалог сам показывает отдельную подсказку,
 * поэтому здесь не нужен empty-state.
 *
 * @param evidence Цитаты узла из контракта.
 * @param approvedIds Множество id уже подтверждённых цитат (оптимистичное состояние страницы).
 * @param onApprove Колбэк подтверждения одной цитаты (реализован на странице с rollback).
 */
function EvidenceList({
  evidence,
  approvedIds,
  onApprove,
}: {
  evidence: Evidence[]
  approvedIds: Set<string>
  onApprove: (evidenceId: string) => void
}) {
  if (evidence.length === 0) return null
  return (
    <ul className="space-y-1.5">
      {evidence.map((item) => {
        const approved = approvedIds.has(item.id)
        return (
          <li
            key={item.id}
            className="flex items-start gap-2 rounded-lg border bg-muted/40 px-3 py-2 text-sm text-muted-foreground"
          >
            <QuoteIcon className="mt-0.5 inline size-3.5 shrink-0" />
            <span className="min-w-0 flex-1">{item.quote}</span>
            {approved ? (
              <span className="flex shrink-0 items-center gap-1 rounded-full bg-primary/10 px-2 py-0.5 text-xs font-medium text-primary">
                <CheckIcon className="size-3" />
                Подтверждено
              </span>
            ) : (
              <Button
                variant="ghost"
                size="sm"
                className="h-6 shrink-0 gap-1 px-2 text-xs"
                onClick={() => onApprove(item.id)}
                aria-label="Подтвердить цитату"
              >
                <CheckIcon className="size-3" />
                Подтвердить
              </Button>
            )}
          </li>
        )
      })}
    </ul>
  )
}

/** Пропсы одной строки дерева. Вынесены в интерфейс, чтобы `memo` сравнивал их стабильно. */
interface RowProps {
  node: TreeNode
  query: string
  selected: boolean
  visibleIds: Set<string>
  expandedIds: Set<string>
  onToggle: (id: string) => void
  onSelect: (node: TreeNode) => void
  onEdit: (node: TreeNode) => void
  onAttach: (node: TreeNode) => void
}

/**
 * Одна строка дерева: шеврон раскрытия (или точка-маркер для листа), кликабельный заголовок,
 * счётчик обоснований и кнопки редактирования/прикрепления.
 *
 * Обёрнут в `memo`: при переключении раскрытия `visibleIds` не меняется (он вне зависимостей
 * фильтрационного `useMemo`), поэтому невинные ветки не перерисовываются по содержимому.
 * Дети рендерятся **только когда узел раскрыт** - это и есть ленивый рендер, дающий
 * «50+ узлов без лагов»: свёрнутое поддерево физически отсутствует в DOM.
 */
const Row = memo(function Row({
  node,
  query,
  selected,
  visibleIds,
  expandedIds,
  onToggle,
  onSelect,
  onEdit,
  onAttach,
}: RowProps) {
  const hasChildren = node.children.length > 0
  const isOpen = expandedIds.has(node.id)
  // Фильтруем детей по видимости: скрытые фильтром узлы не попадаю в рекурсию Row.
  const children = hasChildren ? node.children.filter((child) => visibleIds.has(child.id)) : []
  const evidenceCount = (node.evidence ?? []).length

  return (
    <div>
      <div
        className={`flex items-center gap-1 rounded-lg py-0.5 pr-1 transition-colors hover:bg-muted/60 ${
          selected ? "bg-muted" : ""
        }`}
      >
        {hasChildren ? (
          <Button
            variant="ghost"
            size="icon"
            className="size-6 shrink-0"
            aria-label={isOpen ? `Свернуть узел ${node.title}` : `Развернуть узел ${node.title}`}
            aria-expanded={isOpen}
            onClick={() => onToggle(node.id)}
          >
            <ChevronRightIcon
              className={`size-3.5 transition-transform ${isOpen ? "rotate-90" : ""}`}
            />
          </Button>
        ) : (
          <span className="flex size-6 shrink-0 items-center justify-center">
            <span className="size-1.5 rounded-full bg-muted-foreground" />
          </span>
        )}
        <button
          type="button"
          onClick={() => onSelect(node)}
          aria-label={`Узел ${node.title}: показать обоснования`}
          className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-1.5 py-1 text-left text-sm font-medium transition-colors hover:text-primary"
        >
          <span className="truncate">
            <HighlightedTitle title={node.title} query={query} />
          </span>
          <span className="shrink-0 text-xs font-normal text-muted-foreground">
            {NODE_TYPE_LABELS[node.type]}
          </span>
          {evidenceCount > 0 && (
            <span className="flex shrink-0 items-center gap-0.5 text-xs font-normal text-muted-foreground">
              <QuoteIcon className="size-3" />
              {evidenceCount}
            </span>
          )}
        </button>
        <Button
          variant="ghost"
          size="icon"
          className="size-7 shrink-0"
          onClick={() => onAttach(node)}
          aria-label={`Добавить обоснование к узлу ${node.title}`}
        >
          <PlusIcon className="size-3.5" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="size-7 shrink-0"
          onClick={() => onEdit(node)}
          aria-label={`Редактировать узел ${node.title}`}
        >
          <PencilIcon className="size-3.5" />
        </Button>
      </div>
      {isOpen && children.length > 0 && (
        <div className="ml-6 space-y-0.5 border-l pl-3">
          {children.map((child) => (
            <Row
              key={child.id}
              node={child}
              query={query}
              selected={false}
              visibleIds={visibleIds}
              expandedIds={expandedIds}
              onToggle={onToggle}
              onSelect={onSelect}
              onEdit={onEdit}
              onAttach={onAttach}
            />
          ))}
        </div>
      )}
    </div>
  )
})

/** Пропсы {@link ActivityTree}. Сеть и мутации не здесь - только данные и колбэки страницы. */
export interface ActivityTreeProps {
  /** Плоский массив узлов из контракта; иерархия собирается {@link buildTree} внутри. */
  nodes: ActivityNode[]
  /** Id уже подтверждённых цитат (оптимистичное состояние страницы Research). */
  approvedIds: Set<string>
  /** Подтвердить одну цитату (страница делает запрос с rollback при ошибке). */
  onApprove: (evidenceId: string) => void
  /** Открыть диалог редактирования узла (страница владеет `NodeEditDialog`). */
  onEdit: (node: TreeNode) => void
  /** Открыть диалог прикрепления обоснования (страница владеет `EvidenceAttachDialog`). */
  onAttach: (node: TreeNode) => void
}

/**
 * Экран дерева деятельности: панель управления (поиск, чипы типов, фильтр «с обоснованиями»,
 * развернуть/свернуть всё, счётчик) и рекурсивный список строк {@link Row} с диалогом
 * обоснований по клику на заголовок узла.
 *
 * Состояния: «дерево не построено» обрабатывается **снаружи** (страница Research, когда
 * `nodes` пуст); здесь различается только «фильтры дали пустой результат» -> empty «Ничего
 * не найдено» со сбросом. Все тяжёлые вычисления (сборка иерархии, фильтрация, автораскрытие)
 * мемоизированы, чтобы поллинг страницы не дёргал рендер дерева.
 */
export function ActivityTree({
  nodes,
  approvedIds,
  onApprove,
  onEdit,
  onAttach,
}: ActivityTreeProps) {
  const [query, setQuery] = useState("")
  const [activeTypes, setActiveTypes] = useState<Set<NodeType>>(() => new Set(TYPE_ORDER))
  const [onlyWithEvidence, setOnlyWithEvidence] = useState(false)
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const [selected, setSelected] = useState<TreeNode | null>(null)

  const roots = useMemo(() => buildTree(nodes), [nodes])
  const filtersActive =
    query.trim().length > 0 || onlyWithEvidence || activeTypes.size !== TYPE_ORDER.length

  /**
   * Единый проход фильтрации с разделением структурного и контентных фильтров.
   *
   * - `typePass` - каскадный: невыбранный тип скрывает узел и всё его поддерево
   *   (ранний выход до обхода детей), поэтому выключение «Задача» убирает и действия
   *   под задачей.
   * - `passSearch` / `passEvidence` - контентные цепочки: узел виден, если сам проходит
   *   оба фильтра ИЛИ имеет видимого потомка; предок совпадения остаётся контейнером пути.
   * - `includeVisibleDescendants` показывает поддерево совпавшего узла, но проверяет
   *   `typePass` на каждом спуске, поэтому не протаскивает узлы с выключенным типом
   *   (в отличие от прежнего слепого includeAll).
   *
   * Возвращает `visibleIds` (что рисовать), `matchIds` (узлы, прошедшие оба контентных
   * фильтра - нужны автораскрытию) и `visibleRoots` (корни после фильтра).
   */
  const { visibleIds, matchIds, visibleRoots } = useMemo(() => {
    const q = query.trim().toLowerCase()
    const visible = new Set<string>()
    const matched = new Set<string>()
    const typePass = (node: TreeNode) => activeTypes.has(node.type)
    const passSearch = (node: TreeNode) =>
      q.length === 0 || node.title.toLowerCase().includes(q)
    const passEvidence = (node: TreeNode) =>
      !onlyWithEvidence || (node.evidence ?? []).length > 0
    const includeVisibleDescendants = (node: TreeNode) => {
      if (!typePass(node)) return
      visible.add(node.id)
      node.children.forEach(includeVisibleDescendants)
    }
    const walk = (node: TreeNode): boolean => {
      if (!typePass(node)) return false
      let childVisible = false
      for (const child of node.children) {
        if (walk(child)) childVisible = true
      }
      const selfPass = passSearch(node) && passEvidence(node)
      if (selfPass) matched.add(node.id)
      const show = selfPass || childVisible
      if (show) {
        visible.add(node.id)
        if (filtersActive && selfPass) includeVisibleDescendants(node)
      }
      return show
    }
    const shownRoots = roots.filter((root) => walk(root))
    return { visibleIds: visible, matchIds: matched, visibleRoots: shownRoots }
  }, [roots, query, activeTypes, onlyWithEvidence, filtersActive])

  /**
   * Автораскрытие. Без активных фильтров раскрываем только корни (глубина свёрнута).
   * С фильтрами раскрываем узлы, в поддереве которых есть `matchIds` - узел, прошедший
   * оба контентных фильтра (поиск И обоснования), - чтобы результат был виден без ручного
   * клика. Зависимости подобраны так, чтобы эффект НЕ перезапускался на каждый toggle
   * раскрытия (иначе был бы бесконечный цикл setExpanded -> рендер -> эффект).
   */
  useEffect(() => {
    if (!filtersActive) {
      setExpanded(new Set(roots.map((root) => root.id)))
      return
    }
    const next = new Set<string>()
    const walk = (node: TreeNode): boolean => {
      let hit = matchIds.has(node.id)
      for (const child of node.children) {
        if (walk(child)) hit = true
      }
      if (hit && node.children.length > 0) next.add(node.id)
      return hit
    }
    visibleRoots.forEach(walk)
    setExpanded(next)
  }, [filtersActive, matchIds, visibleRoots, roots])

  const toggle = useCallback((id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

  const expandAll = useCallback(() => {
    const next = new Set<string>()
    const walk = (node: TreeNode) => {
      if (node.children.length > 0) next.add(node.id)
      node.children.forEach(walk)
    }
    visibleRoots.forEach(walk)
    setExpanded(next)
  }, [visibleRoots])

  const collapseAll = useCallback(() => setExpanded(new Set()), [])

  const toggleType = useCallback((type: NodeType) => {
    setActiveTypes((prev) => {
      const next = new Set(prev)
      if (next.has(type)) next.delete(type)
      else next.add(type)
      return next
    })
  }, [])

  const resetFilters = useCallback(() => {
    setQuery("")
    setActiveTypes(new Set(TYPE_ORDER))
    setOnlyWithEvidence(false)
  }, [])

  const select = useCallback((node: TreeNode) => setSelected(node), [])

  const toolbar = (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-40 flex-1">
          <SearchIcon className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Поиск по узлам дерева…"
            className="pl-8"
            aria-label="Поиск по узлам дерева"
          />
        </div>
        <Button variant="outline" size="sm" onClick={expandAll}>
          <ChevronsUpDownIcon className="size-4" />
          Развернуть всё
        </Button>
        <Button variant="outline" size="sm" onClick={collapseAll}>
          <ChevronsDownUpIcon className="size-4" />
          Свернуть всё
        </Button>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {TYPE_ORDER.map((type) => {
          const on = activeTypes.has(type)
          return (
            <Button
              key={type}
              variant={on ? "secondary" : "outline"}
              size="sm"
              aria-pressed={on}
              onClick={() => toggleType(type)}
              className="h-7 px-2.5 text-xs"
            >
              {NODE_TYPE_LABELS[type]}
            </Button>
          )
        })}
        <Button
          variant={onlyWithEvidence ? "secondary" : "outline"}
          size="sm"
          aria-pressed={onlyWithEvidence}
          onClick={() => setOnlyWithEvidence((value) => !value)}
          className="h-7 gap-1 px-2.5 text-xs"
        >
          <QuoteIcon className="size-3" />
          С обоснованиями
        </Button>
        <span className="ml-auto text-xs text-muted-foreground">
          {filtersActive
            ? `Показано ${visibleIds.size} из ${nodes.length}`
            : `Узлов: ${nodes.length}`}
        </span>
      </div>
    </div>
  )

  if (visibleRoots.length === 0) {
    return (
      <div className="space-y-3">
        {toolbar}
        <div className="flex flex-col items-center gap-3 rounded-xl border py-12 text-center">
          <SearchXIcon className="size-6 text-muted-foreground" />
          <p className="font-medium">Ничего не найдено</p>
          <p className="text-sm text-muted-foreground">
            Попробуйте изменить запрос или сбросить фильтры.
          </p>
          <Button variant="outline" size="sm" onClick={resetFilters}>
            Сбросить фильтры
          </Button>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-3">
      {toolbar}
      <div className="space-y-0.5 rounded-xl border p-3">
        {visibleRoots.map((root) => (
          <Row
            key={root.id}
            node={root}
            query={query}
            selected={selected?.id === root.id}
            visibleIds={visibleIds}
            expandedIds={expanded}
            onToggle={toggle}
            onSelect={select}
            onEdit={onEdit}
            onAttach={onAttach}
          />
        ))}
      </div>
      <Dialog open={selected !== null} onOpenChange={(open) => { if (!open) setSelected(null) }}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{selected?.title ?? ""}</DialogTitle>
            <DialogDescription>
              {selected
                ? `${NODE_TYPE_LABELS[selected.type]} · обоснований: ${selected.evidence.length}`
                : ""}
            </DialogDescription>
          </DialogHeader>
          {selected &&
            (selected.evidence.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                К этому узлу пока не прикреплено ни одного обоснования.
              </p>
            ) : (
              <EvidenceList
                evidence={selected.evidence}
                approvedIds={approvedIds}
                onApprove={onApprove}
              />
            ))}
          <div className="flex justify-end">
            <Button
              variant="outline"
              onClick={() => {
                const node = selected
                setSelected(null)
                if (node) onAttach(node)
              }}
            >
              <PlusIcon className="size-4" />
              Добавить обоснование
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}