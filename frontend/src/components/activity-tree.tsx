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
  SearchIcon,
  SearchXIcon,
  XIcon,
} from "lucide-react"
import type { ActivityNode, Evidence, NodeType } from "@/lib/api/client"
import { buildTree, NODE_TYPE_LABELS, type TreeNode } from "./activity-tree-model"

/** Порядок типов для чипов-фильтров: от общего к частному, как в самой методологии дерева. */
const TYPE_ORDER: NodeType[] = ["role", "function", "task", "action"]

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

type EvidenceStatus = NonNullable<Evidence["status"]>

const STATUS_LABEL: Record<EvidenceStatus, string> = {
  pending: "На проверке",
  approved: "Подтверждено",
  rejected: "Отклонено",
}

const STATUS_TONE: Record<EvidenceStatus, string> = {
  pending: "bg-muted text-muted-foreground",
  approved: "bg-success/15 text-success",
  rejected: "bg-destructive/15 text-destructive",
}

function confidenceTone(value: number) {
  if (value >= 0.8) return "bg-success/15 text-success"
  if (value >= 0.5) return "bg-warning/15 text-warning"
  return "bg-destructive/15 text-destructive"
}

/**
 * Список обоснований узла: цитата, бейдж confidence, статус проверки и кнопки
 * «Подтвердить»/«Отклонить». Пустой список рендерит `null` - диалог сам показывает
 * подсказку, поэтому empty-state здесь не нужен.
 *
 * @param evidence Цитаты узла из контракта.
 * @param statuses Оптимистичные статусы со страницы (приоритетнее полей цитаты).
 * @param approvedIds Устаревший набор подтверждённых id (совместимость со старым API).
 * @param onApprove Колбэк подтверждения цитаты.
 * @param onReject Колбэк отклонения цитаты.
 */
function EvidenceList({
  evidence,
  statuses,
  approvedIds,
  onApprove,
  onReject,
}: {
  evidence: Evidence[]
  statuses?: Map<string, EvidenceStatus>
  approvedIds: Set<string>
  onApprove: (evidenceId: string) => void
  onReject?: (evidenceId: string) => void
}) {
  if (evidence.length === 0) return null
  return (
    <ul className="space-y-1.5">
      {evidence.map((item) => {
        const status: EvidenceStatus =
          statuses?.get(item.id) ??
          item.status ??
          (approvedIds.has(item.id) ? "approved" : "pending")
        return (
          <li
            key={item.id}
            className="flex flex-wrap items-center gap-2 rounded-lg border bg-muted/40 px-3 py-2 text-sm"
          >
            <span className="min-w-0 flex-1 text-muted-foreground">{item.quote}</span>
            {typeof item.confidence === "number" && (
              <span
                className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${confidenceTone(item.confidence)}`}
              >
                {Math.round(item.confidence * 100)}%
              </span>
            )}
            <span
              className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_TONE[status]}`}
            >
              {STATUS_LABEL[status]}
            </span>
            {status !== "approved" && (
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
            {status !== "rejected" && onReject && (
              <Button
                variant="ghost"
                size="sm"
                className="h-6 shrink-0 gap-1 px-2 text-xs"
                onClick={() => onReject(item.id)}
                aria-label="Отклонить цитату"
              >
                <XIcon className="size-3" />
                Отклонить
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
          className="flex min-w-0 flex-1 items-baseline gap-2 rounded-md px-1.5 py-1 text-left text-sm font-medium transition-colors hover:text-primary"
        >
          <span className="flex min-w-0 items-baseline gap-2">
            <span className="truncate">
              <HighlightedTitle title={node.title} query={query} />
            </span>
            <span className="shrink-0 text-xs font-normal text-muted-foreground">
              {NODE_TYPE_LABELS[node.type]}
            </span>
          </span>
          {evidenceCount > 0 && (
            <span className="shrink-0 text-xs font-normal text-muted-foreground">
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
  /** Оптимистичные статусы цитат (pending/approved/rejected) со страницы. */
  statuses?: Map<string, EvidenceStatus>
  /** Подтвердить одну цитату (страница делает запрос с rollback при ошибке). */
  onApprove: (evidenceId: string) => void
  /** Отклонить одну цитату (необязательный колбэк: без него кнопки нет). */
  onReject?: (evidenceId: string) => void
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
  statuses,
  onApprove,
  onReject,
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
      // Автораскрытие синхронизирует открытые ветки с результатом фильтрации дерева.
      // eslint-disable-next-line react-hooks/set-state-in-effect
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
          className="h-7 px-2.5 text-xs"
        >
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
                ? `${NODE_TYPE_LABELS[selected.type]}, обоснований: ${selected.evidence.length}`
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
                statuses={statuses}
                onApprove={onApprove}
                onReject={onReject}
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
