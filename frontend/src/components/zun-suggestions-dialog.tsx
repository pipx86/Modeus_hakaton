/**
 * @file zun-suggestions-dialog.tsx
 * @description Rule-based предложения ЗУН из GET /runs/{id}/zun/suggestions с применением.
 */

import { useEffect, useState } from "react"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { apiClient, type ZunSuggestion } from "@/lib/api/client"
import { Loader2Icon, SparklesIcon } from "lucide-react"

export function ZunSuggestionsDialog({
  runId,
  open,
  onOpenChange,
  onApplied,
}: {
  runId: string
  open: boolean
  onOpenChange: (open: boolean) => void
  onApplied?: () => void
}) {
  const [suggestions, setSuggestions] = useState<ZunSuggestion[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [applyingId, setApplyingId] = useState<string | null>(null)
  const [rejectingId, setRejectingId] = useState<string | null>(null)

  useEffect(() => {
    if (!open) return
    let cancelled = false
    void (async () => {
      setLoading(true)
      try {
        const { data, error: requestError } = await apiClient.GET(
          "/runs/{id}/zun/suggestions",
          { params: { path: { id: runId } } },
        )
        if (cancelled) return
        if (requestError) throw new Error(requestError.message)
        setSuggestions(Array.isArray(data) ? data : [])
        setError(null)
      } catch (cause) {
        if (!cancelled) {
          setSuggestions([])
          setError(cause instanceof Error ? cause.message : "Не удалось загрузить предложения.")
        }
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [open, runId])

  const apply = async (suggestion: ZunSuggestion) => {
    setApplyingId(suggestion.node_id)
    try {
      const { data, error: requestError } = await apiClient.POST(
        "/runs/{id}/zun/suggestions/apply",
        {
          params: { path: { id: runId } },
          body: {
            node_id: suggestion.node_id,
            knowledge: suggestion.knowledge,
            skills: suggestion.skills,
            abilities: suggestion.abilities,
          },
        },
      )
      if (requestError) {
        toast.error("Не удалось применить ЗУН", { description: requestError.message })
        return
      }
      setSuggestions((previous) =>
        previous.map((item) =>
          item.node_id === suggestion.node_id
            ? { ...item, can_apply: false, current_zun: data?.current_zun ?? item.current_zun }
            : item,
        ),
      )
      toast.success(`ЗУН применён: ${suggestion.node_title}`)
      onApplied?.()
    } catch {
      toast.error("Сервер недоступен", { description: "ЗУН не сохранён." })
    } finally {
      setApplyingId(null)
    }
  }

  const reject = async (suggestion: ZunSuggestion) => {
    setRejectingId(suggestion.node_id)
    try {
      const { error: requestError } = await apiClient.POST(
        "/runs/{id}/zun/suggestions/reject",
        {
          params: { path: { id: runId } },
          body: { node_id: suggestion.node_id },
        },
      )
      if (requestError) {
        toast.error("Не удалось отклонить предложение", { description: requestError.message })
        return
      }
      setSuggestions((previous) =>
        previous.filter((item) => item.node_id !== suggestion.node_id),
      )
      toast.success(`Предложение скрыто: ${suggestion.node_title}`)
      onApplied?.()
    } catch {
      toast.error("Сервер недоступен", { description: "Отклонение не сохранено." })
    } finally {
      setRejectingId(null)
    }
  }

  const applicable = suggestions.filter((item) => item.can_apply)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[80vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <SparklesIcon className="size-4" />
            Предложения ЗУН
          </DialogTitle>
          <DialogDescription>
            Детерминированные правила по названию и обоснованиям узла. Ручной ЗУН не
            перезаписывается: такие узлы помечены как заполненные.
          </DialogDescription>
        </DialogHeader>

        {loading ? (
          <div className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
            <Loader2Icon className="size-4 animate-spin" />
            Загрузка предложений…
          </div>
        ) : error ? (
          <p className="rounded-lg border border-destructive/50 p-3 text-sm text-destructive">
            {error}
          </p>
        ) : suggestions.length === 0 ? (
          <p className="rounded-lg border border-dashed p-4 text-sm text-muted-foreground">
            Предложений нет: у узлов уже заполнен ЗУН или для них не нашлось правил.
          </p>
        ) : (
          <div className="space-y-3">
            <p className="text-xs text-muted-foreground">
              Можно применить: {applicable.length} из {suggestions.length}
            </p>
            {suggestions.map((item) => (
              <div key={item.node_id} className="space-y-2 rounded-lg border p-3">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{item.node_title}</span>
                  <Badge variant={item.can_apply ? "secondary" : "outline"}>
                    {item.can_apply ? "можно применить" : "ЗУН уже заполнен"}
                  </Badge>
                  <Button
                    size="sm"
                    variant="outline"
                    className="ml-auto h-7 px-2.5 text-xs"
                    disabled={!item.can_apply || applyingId === item.node_id}
                    onClick={() => void apply(item)}
                  >
                    {applyingId === item.node_id ? (
                      <Loader2Icon className="mr-1 size-3.5 animate-spin" />
                    ) : null}
                    Применить
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    className="h-7 px-2.5 text-xs text-muted-foreground"
                    aria-label={`Скрыть предложение ${item.node_title}`}
                    disabled={rejectingId === item.node_id}
                    onClick={() => void reject(item)}
                  >
                    Скрыть
                  </Button>
                </div>
                <div className="grid gap-1 text-xs text-muted-foreground sm:grid-cols-3">
                  <p>
                    <span className="font-medium text-foreground">Знания: </span>
                    {item.knowledge.join("; ") || "-"}
                  </p>
                  <p>
                    <span className="font-medium text-foreground">Умения: </span>
                    {item.skills.join("; ") || "-"}
                  </p>
                  <p>
                    <span className="font-medium text-foreground">Навыки: </span>
                    {item.abilities.join("; ") || "-"}
                  </p>
                </div>
              </div>
            ))}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
