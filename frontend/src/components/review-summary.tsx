/**
 * @file review-summary.tsx
 * @description Сводка проверки исследования: сколько узлов дерева, обоснований и
 * сопоставлений с программой уже проверено.
 *
 * Данные: `GET /runs/{id}/review/summary` -
 * `{nodes, evidence, links}` с полями `{pending, approved, rejected, total}`.
 * Компонент показывает «проверено X из Y» по каждой сущности и полосу прогресса,
 * чтобы итог читался с одного взгляда. Ответ нормализуется: если бэкенд вернул
 * неполный объект (старая версия, ошибка прокси), сводка показывает нули, а не падает.
 *
 * Экспорт: {@link ReviewSummary}.
 */
import { useCallback, useEffect, useState } from "react"
import { ClipboardCheckIcon, RefreshCwIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import {
  ApiRequestError,
  getReviewSummary,
  type ReviewSummary as ReviewSummaryData,
  type ReviewSummarySection,
} from "@/lib/api/client"

const EMPTY_SECTION: ReviewSummarySection = { pending: 0, approved: 0, rejected: 0, total: 0 }

/** Неотрицательное целое из ответа API; всё остальное считаем нулём. */
function toCount(value: unknown): number {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) return 0
  return Math.floor(value)
}

/** Приводит одну секцию сводки к безопасной форме. */
function normalizeSection(value: unknown): ReviewSummarySection {
  if (!value || typeof value !== "object") return EMPTY_SECTION
  const raw = value as Partial<ReviewSummarySection>
  const pending = toCount(raw.pending)
  const approved = toCount(raw.approved)
  const rejected = toCount(raw.rejected)
  const total = toCount(raw.total) || pending + approved + rejected
  return { pending, approved, rejected, total }
}

/** Приводит весь ответ сводки к безопасной форме. */
function normalizeSummary(value: unknown): ReviewSummaryData {
  const raw = (value && typeof value === "object" ? value : {}) as Partial<ReviewSummaryData>
  return {
    nodes: normalizeSection(raw.nodes),
    evidence: normalizeSection(raw.evidence),
    links: normalizeSection(raw.links),
  }
}

/** Понятное сообщение об ошибке сводки; 403/501 объясняются отдельно. */
function describeSummaryError(error: unknown): string {
  if (error instanceof ApiRequestError) {
    if (error.status === 403) return "Сводка проверки доступна методисту."
    if (error.status === 501) return "Сводка проверки пока не включена на сервере."
    if (error.status === 0) return "Сервер недоступен: сводка не загружена."
    return error.message
  }
  return "Не удалось загрузить сводку проверки."
}

/** Одна карточка сводки: «проверено X из Y», прогресс и разбивка по статусам. */
function SummaryCard({
  label,
  section,
  testId,
}: {
  label: string
  section: ReviewSummarySection
  testId: string
}) {
  const reviewed = section.approved + section.rejected
  const percent = section.total > 0 ? Math.round((reviewed / section.total) * 100) : 0
  const approvedWidth = section.total > 0 ? (section.approved / section.total) * 100 : 0
  const rejectedWidth = section.total > 0 ? (section.rejected / section.total) * 100 : 0
  const pendingWidth = section.total > 0 ? (section.pending / section.total) * 100 : 0

  return (
    <div className="space-y-2 rounded-lg border p-3" data-testid={testId}>
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs font-medium text-muted-foreground">{label}</p>
        <span className="text-[11px] text-muted-foreground">{percent}%</span>
      </div>
      <p className="text-sm font-medium">
        проверено {reviewed} из {section.total}
      </p>
      <div
        className="flex h-1.5 overflow-hidden rounded-full bg-muted"
        role="img"
        aria-label={`${label}: проверено ${reviewed} из ${section.total}`}
      >
        <div className="h-full bg-success" style={{ width: `${approvedWidth}%` }} />
        <div className="h-full bg-destructive" style={{ width: `${rejectedWidth}%` }} />
        <div className="h-full bg-warning/60" style={{ width: `${pendingWidth}%` }} />
      </div>
      <p className="text-[11px] text-muted-foreground">
        подтверждено {section.approved} · отклонено {section.rejected} · не проверено{" "}
        {section.pending}
      </p>
    </div>
  )
}

/**
 * Блок сводки проверки над вкладками исследования.
 *
 * @param runId Id исследования.
 */
export function ReviewSummary({ runId }: { runId: string }) {
  const [summary, setSummary] = useState<ReviewSummaryData | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(
    async (silent = false) => {
      if (silent) setRefreshing(true)
      else setLoading(true)
      try {
        const data = await getReviewSummary(runId)
        setSummary(normalizeSummary(data))
        setError(null)
      } catch (cause) {
        setSummary(null)
        setError(describeSummaryError(cause))
      } finally {
        setLoading(false)
        setRefreshing(false)
      }
    },
    [runId],
  )

  useEffect(() => {
    // Загрузка асинхронная: состояние обновится после ответа API.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load()
  }, [load])

  return (
    <Card className="gap-3 p-4">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="space-y-0.5">
          <p className="flex items-center gap-2 text-sm font-medium">
            <ClipboardCheckIcon className="size-4 text-primary" />
            Результат проверки
          </p>
          <p className="text-xs text-muted-foreground">
            Сколько узлов, обоснований и сопоставлений уже проверено.
          </p>
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Обновить сводку проверки"
          disabled={loading || refreshing}
          onClick={() => void load(true)}
        >
          <RefreshCwIcon className={`size-4 ${refreshing ? "animate-spin" : ""}`} />
        </Button>
      </div>

      {loading ? (
        <div className="grid gap-3 sm:grid-cols-3">
          {[0, 1, 2].map((index) => (
            <Skeleton key={index} className="h-24 w-full" />
          ))}
        </div>
      ) : error ? (
        <div className="space-y-2 rounded-lg border border-destructive/50 p-3 text-sm">
          <p className="text-destructive">{error}</p>
          <Button variant="outline" size="sm" onClick={() => void load()}>
            Повторить
          </Button>
        </div>
      ) : summary ? (
        <div className="grid gap-3 sm:grid-cols-3">
          <SummaryCard label="Узлы" section={summary.nodes} testId="review-summary-nodes" />
          <SummaryCard
            label="Обоснования"
            section={summary.evidence}
            testId="review-summary-evidence"
          />
          <SummaryCard
            label="Сопоставления"
            section={summary.links}
            testId="review-summary-links"
          />
        </div>
      ) : null}
    </Card>
  )
}
