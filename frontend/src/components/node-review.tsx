/**
 * @file node-review.tsx
 * @description Блок проверки узла дерева методистом: текущий статус, комментарий
 * методиста и решение «Подтвердить»/«Отклонить» с необязательным комментарием.
 *
 * Компонент вызывается из панели выбранного узла {@link ActivityTree}, поэтому
 * цитаты-основания узла остаются на экране: проверка не заменяет и не скрывает их,
 * а добавляет отдельную секцию над списком обоснований.
 *
 * Запрос идёт в `POST /nodes/{id}/review` через {@link setNodeReview}. После успеха
 * статус обновляется локально (бейдж не врёт) и вызывается `onReviewed` - дерево
 * сохраняет новое состояние узла, чтобы повторное открытие панели показывало решение.
 * Ошибки 4xx/5xx остаются в блоке проверки и не перекрывают цитаты.
 *
 * Экспорт: {@link NodeReview}.
 */
import { useState } from "react"
import { CheckIcon, Loader2Icon, XIcon } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import {
  ApiRequestError,
  setNodeReview,
  type NodeReviewDecision,
  type NodeReviewFields,
  type NodeReviewStatus,
  type ReviewableNode,
} from "@/lib/api/client"

/** Русские подписи статусов проверки узла. */
const REVIEW_STATUS_LABEL: Record<NodeReviewStatus, string> = {
  pending: "не проверен",
  approved: "подтверждён",
  rejected: "отклонён",
}

const REVIEW_STATUS_CLASS: Record<NodeReviewStatus, string> = {
  pending: "bg-muted text-muted-foreground",
  approved: "bg-success/15 text-success",
  rejected: "bg-destructive/15 text-destructive",
}

/** Поля проверки узла: принимает и узел из дерева, и ответ `POST /nodes/{id}/review`. */
export interface NodeReviewProps {
  nodeId: string
  review?: NodeReviewFields | null
  /** Режим просмотра: статус и комментарий видны, кнопки и поле ввода скрыты. */
  readOnly?: boolean
  /** Вызывается после успешного решения - дерево обновляет локальное состояние узла. */
  onReviewed?: (node: ReviewableNode) => void
}

/** Безопасно приводит неизвестный статус к известному (старый бэкенд мог не отдать поле). */
function normalizeStatus(value?: string | null): NodeReviewStatus {
  if (value === "approved" || value === "rejected") return value
  return "pending"
}

/** Короткая дата решения; неразобранное значение возвращается как есть. */
function formatReviewedAt(value?: string | null): string {
  if (!value) return ""
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat("ru-RU", {
    day: "numeric",
    month: "short",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date)
}

/** Понятное сообщение об ошибке решения: 403/404/сеть объясняются отдельно. */
function describeReviewError(error: unknown): string {
  if (error instanceof ApiRequestError) {
    if (error.status === 403) return "Недостаточно прав: проверять узлы может только методист."
    if (error.status === 404) return "Узел не найден: возможно, его уже удалили."
    if (error.status === 0) return "Сервер недоступен: решение не сохранено."
    if (error.status === 501) return "Проверка узлов пока не включена на сервере."
    return error.message
  }
  return "Не удалось сохранить решение. Попробуйте ещё раз."
}

/**
 * Секция проверки узла: бейдж «не проверен»/«подтверждён»/«отклонён», комментарий
 * методиста (если он есть) и форма решения. Основания узла рендерит родитель -
 * этот блок их не трогает.
 */
export function NodeReview({ nodeId, review, readOnly = false, onReviewed }: NodeReviewProps) {
  const [current, setCurrent] = useState<NodeReviewFields>(review ?? {})
  const [comment, setComment] = useState("")
  const [busy, setBusy] = useState<NodeReviewDecision | null>(null)
  const [error, setError] = useState<string | null>(null)

  const status = normalizeStatus(current.review_status)
  const reviewedAt = formatReviewedAt(current.reviewed_at)
  const methodistComment = current.review_comment?.trim()

  const submit = async (decision: NodeReviewDecision) => {
    if (busy) return
    setBusy(decision)
    setError(null)
    try {
      const updated = await setNodeReview(nodeId, decision, comment.trim())
      const next: NodeReviewFields = {
        review_status: normalizeStatus(updated?.review_status ?? decision),
        review_comment: updated?.review_comment ?? comment.trim(),
        reviewed_at: updated?.reviewed_at ?? new Date().toISOString(),
      }
      setCurrent(next)
      setComment("")
      onReviewed?.({ ...(updated as ReviewableNode), ...next, id: updated?.id ?? nodeId })
    } catch (cause) {
      setError(describeReviewError(cause))
    } finally {
      setBusy(null)
    }
  }

  return (
    <section className="space-y-2.5 rounded-lg border bg-muted/20 p-3" aria-label="Проверка узла">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs font-medium text-muted-foreground">Проверка узла</span>
        <Badge variant="outline" className={REVIEW_STATUS_CLASS[status]}>
          {REVIEW_STATUS_LABEL[status]}
        </Badge>
        {reviewedAt && (
          <span className="text-[11px] text-muted-foreground">решение от {reviewedAt}</span>
        )}
      </div>

      {methodistComment && (
        <p className="rounded-md bg-background/70 px-2 py-1.5 text-xs text-muted-foreground">
          <span className="font-medium text-foreground">Комментарий методиста: </span>
          {methodistComment}
        </p>
      )}

      {!readOnly && (
        <div className="space-y-2">
          <div className="space-y-1">
            <Label htmlFor={`node-review-comment-${nodeId}`}>
              Комментарий к решению (необязательно)
            </Label>
            <Textarea
              id={`node-review-comment-${nodeId}`}
              value={comment}
              onChange={(event) => setComment(event.target.value)}
              placeholder="Почему решение принято - увидит методист"
              className="min-h-14 text-sm"
              disabled={busy !== null}
            />
          </div>
          {error && (
            <p className="text-sm text-destructive" role="alert">
              {error}
            </p>
          )}
          <div className="flex flex-wrap gap-2">
            <Button
              size="sm"
              variant={status === "approved" ? "secondary" : "default"}
              disabled={busy !== null}
              onClick={() => void submit("approved")}
            >
              {busy === "approved" ? (
                <Loader2Icon className="size-3.5 animate-spin" />
              ) : (
                <CheckIcon className="size-3.5" />
              )}
              Подтвердить
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={busy !== null}
              onClick={() => void submit("rejected")}
            >
              {busy === "rejected" ? (
                <Loader2Icon className="size-3.5 animate-spin" />
              ) : (
                <XIcon className="size-3.5" />
              )}
              Отклонить
            </Button>
          </div>
          <p className="text-[11px] text-muted-foreground">
            Основания узла остаются на месте: проверка не удаляет цитаты.
          </p>
        </div>
      )}
    </section>
  )
}
