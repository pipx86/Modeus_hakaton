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
import { memo, useCallback, useEffect, useMemo, useState, type KeyboardEvent } from "react"
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
  AlertTriangleIcon,
  CheckIcon,
  ChevronRightIcon,
  ChevronsDownUpIcon,
  ChevronsUpDownIcon,
  HeartPulseIcon,
  MessageSquareIcon,
  PencilIcon,
  PlusIcon,
  SearchIcon,
  SearchXIcon,
  XIcon,
} from "lucide-react"
import { toast } from "sonner"
import type { ActivityNode, Evidence, Insights, NodeType, Source } from "@/lib/api/client"
import { SOURCE_PRIORITY, explainNode, provenanceFor, provenanceLabel, type ProvenanceInfo } from "@/lib/provenance"
import { computeTreeFlags, type TreeFlag, type TreeFlagCode } from "@/lib/tree-flags"
import { SOURCE_TYPE_LABEL } from "@/lib/status"
import { buildTree, NODE_TYPE_LABELS, type TreeNode } from "./activity-tree-model"

/** Порядок типов для чипов-фильтров: от общего к частному, как в самой методологии дерева. */
const TYPE_ORDER: NodeType[] = ["role", "function", "task", "action"]

/** Визуальный код типа узла: цветная точка и мягкий бейдж. */
const TYPE_STYLE: Record<NodeType, { dot: string; badge: string }> = {
  role: { dot: "bg-violet-400", badge: "bg-violet-500/10 text-violet-300" },
  function: { dot: "bg-sky-400", badge: "bg-sky-500/10 text-sky-300" },
  task: { dot: "bg-amber-400", badge: "bg-amber-500/10 text-amber-300" },
  action: { dot: "bg-emerald-400", badge: "bg-emerald-500/10 text-emerald-300" },
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

/** Серверные коды red flags → клиентские подписи и локальные коды. */
const SERVER_FLAG_META: Record<string, { code: TreeFlagCode; label: string }> = {
  SUSPICIOUS_TITLE: { code: "generic_title", label: "Подозрительное название" },
  DUPLICATE_TITLE: { code: "duplicate_title", label: "Дубль" },
  SINGLE_WEAK_SOURCE: { code: "weak_source", label: "Слабый источник" },
  NO_CHILDREN: { code: "no_children", label: "Нет детей" },
  NO_EVIDENCE: { code: "no_evidence", label: "Нет обоснования" },
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
  sources,
  onApprove,
  onReject,
  onShowTranscript,
  readOnly = false,
}: {
  evidence: Evidence[]
  statuses?: Map<string, EvidenceStatus>
  approvedIds: Set<string>
  sources?: Map<string, Source>
  onApprove: (evidenceId: string) => void
  onReject?: (evidenceId: string) => void
  onShowTranscript?: (item: Evidence, source?: Source) => void
  readOnly?: boolean
}) {
  if (evidence.length === 0) return null
  return (
    <ul className="space-y-1.5">
      {evidence.map((item) => {
        const status: EvidenceStatus =
          statuses?.get(item.id) ??
          item.status ??
          (approvedIds.has(item.id) ? "approved" : "pending")
        // confidence приходит только из legacy-моков; контракт API его не гарантирует.
        const confidence = (item as Evidence & { confidence?: number }).confidence
        const source = sources?.get(item.source_id)
        return (
          <li
            key={item.id}
            className="flex flex-wrap items-center gap-2 rounded-lg border bg-muted/40 px-3 py-2 text-sm"
          >
            {source && (
              <span className="flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground">
                <span className="rounded-full bg-background/70 px-1.5 py-0.5">
                  {SOURCE_TYPE_LABEL[source.type] ?? source.type}
                </span>
                <span className="max-w-40 truncate" title={source.title}>
                  {source.title}
                </span>
                <span>· {SOURCE_PRIORITY[source.type]}</span>
              </span>
            )}
            <span className="min-w-0 flex-1 whitespace-pre-wrap break-words text-muted-foreground">{item.quote}</span>
            {typeof confidence === "number" && (
              <span
                className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${confidenceTone(confidence)}`}
              >
                {Math.round(confidence * 100)}%
              </span>
            )}
            <span
              className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_TONE[status]}`}
            >
              {STATUS_LABEL[status]}
            </span>
            {onShowTranscript && source?.type === "interview" && (
              <Button
                variant="ghost"
                size="sm"
                className="h-6 shrink-0 gap-1 px-2 text-xs"
                onClick={() => onShowTranscript(item, source)}
              >
                <MessageSquareIcon className="size-3" />
                В интервью
              </Button>
            )}
            {status !== "approved" && !readOnly && (
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
            {status !== "rejected" && onReject && !readOnly && (
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
  depth: number
  position: number
  total: number
  hasChildren: boolean
  isOpen: boolean
  query: string
  selected: boolean
  onToggle: (id: string) => void
  onSelect: (node: TreeNode) => void
  onEdit: (node: TreeNode) => void
  onAttach: (node: TreeNode) => void
  onShowZun: (node: TreeNode) => void
  provenance?: ProvenanceInfo
  flags?: TreeFlag[]
  heat?: { score: number; matches: string[] }
  heatLevel?: number
  readOnly?: boolean
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
  depth,
  position,
  total,
  hasChildren,
  isOpen,
  query,
  selected,
  onToggle,
  onSelect,
  onEdit,
  onAttach,
  onShowZun,
  provenance,
  flags,
  heat,
  heatLevel = 0,
  readOnly = false,
}: RowProps) {
  const evidenceCount = (node.evidence ?? []).length
  const zun = node.zun
  const hasZun = !!zun && zun.knowledge.length + zun.skills.length + zun.abilities.length > 0

  const typeStyle = TYPE_STYLE[node.type]

  return (
    <div
      data-row
      role="treeitem"
      aria-level={depth + 1}
      aria-posinset={position}
      aria-setsize={total}
      aria-expanded={hasChildren ? isOpen : undefined}
      className={`tree-node-enter group relative flex h-11 items-center gap-2 rounded-lg pr-1.5 transition-colors hover:bg-muted/50 ${
        selected ? "bg-muted/70 ring-1 ring-border" : ""
      } ${
        heatLevel === 3
          ? "bg-warning/15"
          : heatLevel === 2
            ? "bg-warning/10"
            : heatLevel === 1
              ? "bg-warning/5"
              : ""
      }`}
      style={{ paddingLeft: Math.min(depth * 24, 96), animationDelay: `${Math.min(depth, 6) * 45}ms` }}
    >
      {Array.from({ length: depth }).map((_, index) => (
        <span
          key={index}
          aria-hidden
          className="absolute top-0 h-full w-px bg-border/60"
          style={{ left: index * 24 + 12 }}
        />
      ))}

      {hasChildren ? (
        <Button
          variant="ghost"
          size="icon"
          className="size-6 shrink-0 rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
          aria-label={isOpen ? `Свернуть узел ${node.title}` : `Развернуть узел ${node.title}`}
          aria-expanded={isOpen}
          onClick={() => onToggle(node.id)}
        >
          <ChevronRightIcon
            className={`size-4 transition-transform duration-150 ${isOpen ? "rotate-90" : ""}`}
          />
        </Button>
      ) : (
        <span className="flex size-6 shrink-0 items-center justify-center">
          <span className={`size-1.5 rounded-full ${typeStyle.dot}`} />
        </span>
      )}

      <button
        type="button"
        onClick={() => onSelect(node)}
        aria-label={`Узел ${node.title}: показать обоснования`}
        className="flex min-w-0 flex-1 items-center gap-2.5 rounded-md px-1 py-1 text-left text-sm transition-colors hover:text-primary"
      >
        <span className={`truncate ${node.type === "role" ? "font-semibold" : "font-medium"}`}>
          <HighlightedTitle title={node.title} query={query} />
        </span>
        <span
          className={`shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium ${typeStyle.badge}`}
        >
          {NODE_TYPE_LABELS[node.type]}
        </span>
        {provenance && provenanceLabel(provenance) && (
          <span
            className="shrink-0 rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground"
            title={provenance.labels.length > 0 ? provenance.labels.join(", ") : "источник без метаданных"}
          >
            {provenanceLabel(provenance)}
          </span>
        )}
        {flags && flags.length > 0 && (
          <span
            className="flex shrink-0 items-center gap-1 text-[10px] font-medium text-destructive"
            title={flags.map((flag) => flag.reason).join(" ")}
            aria-label={`Проблемы узла: ${flags.map((flag) => flag.label).join(", ")}`}
          >
            <AlertTriangleIcon className="size-3.5" />
            {flags.length}
          </span>
        )}
        {heat && heat.score > 0 && (
          <span
            className="shrink-0 rounded-full bg-warning/15 px-1.5 py-0.5 text-[10px] font-medium text-warning"
            title={`Рынок: ${heat.matches.join(", ")} (${heat.score})`}
          >
            рынок ×{heat.score}
          </span>
        )}
        {evidenceCount > 0 && (
          <span className="shrink-0 text-[11px] text-muted-foreground">{evidenceCount}</span>
        )}
      </button>

      {hasZun && (
        <Button
          variant="ghost"
          size="sm"
          className="h-6 shrink-0 rounded-full bg-primary/10 px-2 text-[11px] text-primary hover:bg-primary/20"
          aria-label={`ЗУН узла ${node.title}`}
          onClick={() => onShowZun(node)}
        >
          ЗУН
        </Button>
      )}

      {!readOnly && (
      <div className="flex shrink-0 items-center opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
        <Button
          variant="ghost"
          size="icon"
          className="size-7"
          onClick={() => onAttach(node)}
          aria-label={`Добавить обоснование к узлу ${node.title}`}
        >
          <PlusIcon className="size-3.5" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="size-7"
          onClick={() => onEdit(node)}
          aria-label={`Редактировать узел ${node.title}`}
        >
          <PencilIcon className="size-3.5" />
        </Button>
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
  /** Узел из ссылки `?node=`: раскрыть ветку и подсветить строку. */
  focusNodeId?: string | null
  /** Источники прогона: типы и приоритеты для провенанса и объяснения узла. */
  sourcesById?: Map<string, Source>
  /** Перейти к цитате в интервью (вкладка чата и подсветка сообщения). */
  onShowTranscript?: (item: Evidence, source?: Source) => void
  /** Серверные инсайты: здоровье дерева, провенанс, флаги, рынок и объяснения узлов. */
  insights?: Insights | null
  /** Просмотр без изменений (режим ?view=readonly). */
  readOnly?: boolean
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
  focusNodeId,
  sourcesById,
  onShowTranscript,
  insights,
  readOnly = false,
}: ActivityTreeProps) {
  const [query, setQuery] = useState("")
  const [activeTypes, setActiveTypes] = useState<Set<NodeType>>(() => new Set(TYPE_ORDER))
  const [onlyWithEvidence, setOnlyWithEvidence] = useState(false)
  const [onlyWithZun, setOnlyWithZun] = useState(false)
  const [onlyProblem, setOnlyProblem] = useState(false)
  const [zunNode, setZunNode] = useState<TreeNode | null>(null)
  const [scrollTop, setScrollTop] = useState(0)
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const [selected, setSelected] = useState<TreeNode | null>(null)

  const roots = useMemo(() => buildTree(nodes), [nodes])
  const flagsByNode = useMemo(() => {
    if (!insights) return computeTreeFlags(nodes, sourcesById)
    const map = new Map<string, TreeFlag[]>()
    for (const item of Array.isArray(insights.flags) ? insights.flags : []) {
      map.set(
        item.node_id,
        item.codes.map((code, index) => {
          const meta = SERVER_FLAG_META[code] ?? {
            code: "no_evidence" as const,
            label: code,
          }
          return { ...meta, reason: item.reasons[index] ?? "" }
        }),
      )
    }
    return map
  }, [insights, nodes, sourcesById])
  const heatByNode = insights?.market_heat?.nodes ?? {}
  const maxHeat = Object.values(heatByNode).reduce((max, item) => Math.max(max, item.score), 0)
  const [healthOpen, setHealthOpen] = useState(false)
  const provenanceForNode = (node: TreeNode): ProvenanceInfo => {
    const server = insights?.provenance?.nodes?.[node.id]
    if (server) {
      return {
        types: server.source_types,
        labels: server.source_types.map((type) => SOURCE_TYPE_LABEL[type] ?? type),
        count: server.source_types.length,
        unknown: 0,
      }
    }
    return provenanceFor(node.evidence ?? [], sourcesById)
  }
  const flagsTotal = flagsByNode.size
  const healthScore = insights?.health.score
  const healthTone =
    healthScore === undefined
      ? "text-muted-foreground"
      : healthScore >= 80
        ? "text-success"
        : healthScore >= 60
          ? "text-warning"
          : "text-destructive"
  const filtersActive =
    query.trim().length > 0 ||
    onlyWithEvidence ||
    onlyWithZun ||
    onlyProblem ||
    activeTypes.size !== TYPE_ORDER.length

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
    const passZun = (node: TreeNode) => !onlyWithZun || !!node.zun
    const passFlags = (node: TreeNode) =>
      !onlyProblem || (flagsByNode.get(node.id)?.length ?? 0) > 0
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
      const selfPass = passSearch(node) && passEvidence(node) && passZun(node) && passFlags(node)
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
  }, [roots, query, activeTypes, onlyWithEvidence, onlyWithZun, onlyProblem, flagsByNode, filtersActive])

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

  /**
   * Плоский список видимых строк. Глубина и раскрытие считаются здесь, чтобы рендер
   * оставался одномерным: это и позволяет виртуализировать дерево по индексам.
   */
  const rows = useMemo(() => {
    const out: { node: TreeNode; depth: number; hasChildren: boolean; isOpen: boolean }[] = []
    const walk = (node: TreeNode, depth: number) => {
      if (!visibleIds.has(node.id)) return
      const isOpen = expanded.has(node.id)
      out.push({ node, depth, hasChildren: node.children.length > 0, isOpen })
      if (isOpen) {
        node.children
          .filter((child) => visibleIds.has(child.id))
          .forEach((child) => walk(child, depth + 1))
      }
    }
    visibleRoots.forEach((root) => walk(root, 0))
    return out
  }, [visibleRoots, visibleIds, expanded])

  const VIRTUAL_THRESHOLD = 60
  const ROW_HEIGHT = 44
  const VIEWPORT = 560
  const virtualized = rows.length > VIRTUAL_THRESHOLD
  const start = virtualized ? Math.max(0, Math.floor(scrollTop / ROW_HEIGHT) - 4) : 0
  const end = virtualized
    ? Math.min(rows.length, start + Math.ceil(VIEWPORT / ROW_HEIGHT) + 8)
    : rows.length

  useEffect(() => {
    if (!focusNodeId) return
    const byId = new Map(nodes.map((node) => [node.id, node]))
    const chain = new Set<string>()
    let current = byId.get(focusNodeId)
    while (current?.parent_id) {
      chain.add(current.parent_id)
      current = byId.get(current.parent_id)
    }
    if (chain.size > 0) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setExpanded((prev) => new Set([...prev, ...chain]))
    }
  }, [focusNodeId, nodes])

  useEffect(() => {
    if (!focusNodeId) return
    const index = rows.findIndex((row) => row.node.id === focusNodeId)
    if (index >= 0) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setScrollTop(Math.max(0, index * ROW_HEIGHT - VIEWPORT / 2))
    }
  }, [focusNodeId, rows])

  const handleTreeKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return
    event.preventDefault()
    const rowEls = Array.from(document.querySelectorAll<HTMLElement>("[data-row]"))
    if (rowEls.length === 0) return
    const focusRow = (row?: HTMLElement) => row?.querySelector<HTMLElement>("button")?.focus()

    if (event.key === "Home") {
      setScrollTop(0)
      focusRow(rowEls[0])
      return
    }
    if (event.key === "End") {
      setScrollTop(rows.length * ROW_HEIGHT)
      focusRow(rowEls[rowEls.length - 1])
      return
    }
    const activeRow = document.activeElement?.closest<HTMLElement>("[data-row]") ?? null
    const index = activeRow ? rowEls.indexOf(activeRow) : -1
    focusRow(rowEls[index + (event.key === "ArrowDown" ? 1 : -1)])
  }

  const resetFilters = useCallback(() => {
    setQuery("")
    setActiveTypes(new Set(TYPE_ORDER))
    setOnlyWithEvidence(false)
    setOnlyWithZun(false)
    setOnlyProblem(false)
  }, [])

  const select = useCallback((node: TreeNode) => setSelected(node), [])

  const toolbar = (
    <div className="space-y-3" data-print-hide>
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
        <Button
          variant={onlyWithZun ? "secondary" : "outline"}
          size="sm"
          aria-pressed={onlyWithZun}
          onClick={() => setOnlyWithZun((value) => !value)}
          className="h-7 px-2.5 text-xs"
        >
          С ЗУН
        </Button>
        <Button
          variant={onlyProblem ? "secondary" : "outline"}
          size="sm"
          aria-pressed={onlyProblem}
          onClick={() => setOnlyProblem((value) => !value)}
          className="h-7 gap-1 px-2.5 text-xs"
          title="Показать узлы с замечаниями качества"
        >
          <AlertTriangleIcon className="size-3.5" />
          Проблемные
          {flagsTotal > 0 && <span className="text-muted-foreground">{flagsTotal}</span>}
        </Button>
        {insights && (
          <Button
            variant="outline"
            size="sm"
            className={`h-7 gap-1 px-2.5 text-xs ${healthTone}`}
            onClick={() => setHealthOpen(true)}
            aria-label={`Здоровье дерева: ${insights.health.score} из 100`}
          >
            <HeartPulseIcon className="size-3.5" />
            Здоровье: {insights.health.score}
          </Button>
        )}
        <span className="ml-auto text-xs text-muted-foreground">
          {filtersActive
            ? `Показано ${visibleIds.size} из ${nodes.length}`
            : `Узлов: ${nodes.length}`}
        </span>
      </div>
      {insights && (insights.market_heat?.top?.length ?? 0) > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 text-[11px] text-muted-foreground">
          <span>Тепло рынка:</span>
          {insights.market_heat.top.slice(0, 10).map((item) => (
            <span
              key={item.skill}
              className="rounded-full bg-warning/10 px-2 py-0.5 text-warning"
              title={`${item.skill}: ${item.frequency}`}
            >
              {item.skill} · {item.frequency}
            </span>
          ))}
          <span className="inline-flex items-center gap-1">
            реже
            <span className="h-1.5 w-12 rounded-full bg-gradient-to-r from-warning/10 to-warning" />
            чаще
          </span>
          {insights.market_heat.source === "vacancy_sources" && insights.market_heat.sample_size ? (
            <span>· по {insights.market_heat.sample_size} источникам</span>
          ) : null}
        </div>
      )}
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
      <div
        className="max-h-[560px] space-y-0.5 overflow-y-auto rounded-xl border p-3"
        role="tree"
        aria-label="Дерево деятельности"
        tabIndex={0}
        onKeyDown={handleTreeKeyDown}
        data-virtualized={virtualized ? "true" : "false"}
        data-tree-scroll
        onScroll={(event) => setScrollTop(event.currentTarget.scrollTop)}
      >
        {start > 0 && <div style={{ height: start * ROW_HEIGHT }} aria-hidden />}
        {rows.slice(start, end).map((row, index) => {
          const heat = heatByNode[row.node.id]
          const heatLevel =
            heat && maxHeat > 0 ? Math.max(1, Math.ceil((heat.score / maxHeat) * 3)) : 0
          return (
            <Row
              key={row.node.id}
              node={row.node}
              depth={row.depth}
              position={start + index + 1}
              total={rows.length}
              hasChildren={row.hasChildren}
              isOpen={row.isOpen}
              query={query}
              selected={selected?.id === row.node.id || row.node.id === focusNodeId}
              onToggle={toggle}
              onSelect={select}
              onEdit={onEdit}
              onAttach={onAttach}
              onShowZun={setZunNode}
              provenance={provenanceForNode(row.node)}
              flags={flagsByNode.get(row.node.id)}
              heat={heat}
              heatLevel={heatLevel}
              readOnly={readOnly}
            />
          )
        })}
        {end < rows.length && (
          <div style={{ height: (rows.length - end) * ROW_HEIGHT }} aria-hidden />
        )}
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
          {selected && (
            <div className="space-y-3">
              <p className="rounded-lg bg-muted/40 p-3 text-xs text-muted-foreground">
                {insights?.node_explanations?.[selected.id]?.summary ??
                  explainNode(selected.title, selected.evidence, sourcesById)}
              </p>
              {(flagsByNode.get(selected.id)?.length ?? 0) > 0 && (
                <ul className="space-y-1 rounded-lg border border-destructive/40 bg-destructive/5 p-2 text-xs">
                  {flagsByNode.get(selected.id)!.map((flag) => (
                    <li key={flag.code}>
                      <span className="font-medium text-destructive">{flag.label}: </span>
                      <span className="text-muted-foreground">{flag.reason}</span>
                    </li>
                  ))}
                </ul>
              )}
              {selected.evidence.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  К этому узлу пока не прикреплено ни одного обоснования.
                </p>
              ) : (
                <EvidenceList
                  evidence={selected.evidence}
                  approvedIds={approvedIds}
                  statuses={statuses}
                  sources={sourcesById}
                  onApprove={onApprove}
                  onReject={onReject}
                  onShowTranscript={onShowTranscript}
                  readOnly={readOnly}
                />
              )}
            </div>
          )}
          <div className="flex justify-end gap-2">
            <Button
              variant="ghost"
              onClick={() => {
                if (!selected) return
                const url = `${window.location.origin}${window.location.pathname}?node=${selected.id}`
                void navigator.clipboard?.writeText(url)
                toast.success("Ссылка на узел скопирована")
              }}
            >
              Скопировать ссылку
            </Button>
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

      <Dialog open={zunNode !== null} onOpenChange={(open) => { if (!open) setZunNode(null) }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{zunNode?.title ?? ""}</DialogTitle>
            <DialogDescription>Знания, умения и навыки для этого действия</DialogDescription>
          </DialogHeader>
          {zunNode && (
            <div className="space-y-3 text-sm">
              {(
                [
                  ["Знания", zunNode.zun?.knowledge ?? []],
                  ["Умения", zunNode.zun?.skills ?? []],
                  ["Навыки", zunNode.zun?.abilities ?? []],
                ] as const
              ).map(([label, items]) => (
                <div key={label}>
                  <p className="mb-1 text-xs font-medium text-muted-foreground">{label}</p>
                  {items.length === 0 ? (
                    <p className="text-muted-foreground">Нет</p>
                  ) : (
                    <ul className="list-disc space-y-0.5 pl-5">
                      {items.map((item) => (
                        <li key={item}>{item}</li>
                      ))}
                    </ul>
                  )}
                </div>
              ))}
            </div>
          )}
          {zunNode && (
            <div className="flex justify-end">
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  const lines = [
                    `${zunNode.title} (${NODE_TYPE_LABELS[zunNode.type]})`,
                    "",
                    `Знания: ${(zunNode.zun?.knowledge ?? []).join("; ") || "нет"}`,
                    `Умения: ${(zunNode.zun?.skills ?? []).join("; ") || "нет"}`,
                    `Навыки: ${(zunNode.zun?.abilities ?? []).join("; ") || "нет"}`,
                  ]
                  if (zunNode.evidence.length > 0) {
                    lines.push("", `Цитата: «${zunNode.evidence[0].quote}»`)
                  }
                  void navigator.clipboard?.writeText(lines.join("\n"))
                  toast.success("Паспорт компетенции скопирован")
                }}
              >
                Скопировать паспорт
              </Button>
            </div>
          )}
        </DialogContent>
      </Dialog>
      <Dialog open={healthOpen} onOpenChange={setHealthOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Здоровье дерева</DialogTitle>
            <DialogDescription>
              {insights ? `${insights.health.score} из 100. ${insights.health.formula}` : ""}
            </DialogDescription>
          </DialogHeader>
          {insights && (
            <div className="space-y-3">
              {(
                [
                  ["groundedness", "Обоснованность"],
                  ["completeness", "Полнота"],
                  ["duplicates", "Отсутствие дублей"],
                  ["orphans", "Отсутствие сирот"],
                ] as const
              ).map(([key, label]) => {
                const value = insights.health.breakdown[key]
                return (
                  <div key={key} className="space-y-1">
                    <div className="flex items-center justify-between text-xs">
                      <span className="text-muted-foreground">{label}</span>
                      <span className="font-medium">{value}</span>
                    </div>
                    <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                      <div
                        className={`h-full rounded-full ${
                          value >= 80 ? "bg-success" : value >= 60 ? "bg-warning" : "bg-destructive"
                        }`}
                        style={{ width: `${Math.max(0, Math.min(100, value))}%` }}
                      />
                    </div>
                  </div>
                )
              })}
              <p className="text-xs text-muted-foreground">
                Узлов: {insights.health.counts.node_count}, с обоснованием:{" "}
                {insights.health.counts.grounded_nodes}, дублей:{" "}
                {insights.health.counts.duplicate_nodes}, сирот:{" "}
                {insights.health.counts.orphan_nodes}, без детей:{" "}
                {insights.health.counts.childless_nodes}.
              </p>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}
