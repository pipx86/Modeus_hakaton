/**
 * @file conflicts-tab.tsx
 * @description Вкладка «Конфликты»: какие значения источников противоречили друг другу
 * и какое из них выбрал детерминированный синтезатор.
 */

import { useCallback, useEffect, useState } from "react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { apiClient, type Conflict } from "@/lib/api/client"
import { SOURCE_TYPE_LABEL } from "@/lib/status"
import { RefreshCwIcon, ScaleIcon } from "lucide-react"

const FIELD_LABELS: Record<string, string> = {
  title: "Название",
  description: "Описание",
}

interface ConflictsTabProps {
  runId: string
  onFocusNode?: (nodeId: string) => void
}

/**
 * Показывает разрешённые конфликты синтезатора. Раздел доступен администратору,
 * поэтому для остальных ролей выводится понятное объяснение вместо пустого экрана.
 */
export function ConflictsTab({ runId, onFocusNode }: ConflictsTabProps) {
  const [items, setItems] = useState<Conflict[]>([])
  // Названия узлов по id: методисту нужен текст узла, а не его UUID.
  const [nodeTitles, setNodeTitles] = useState<Record<string, string>>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const { data, error: apiError } = await apiClient.GET("/runs/{id}/conflicts", {
        params: { path: { id: runId } },
      })
      if (apiError) {
        setItems([])
        setError(
          apiError.code === "FORBIDDEN"
            ? "Раздел доступен администратору."
            : (apiError.message ?? "Не удалось загрузить конфликты."),
        )
        return
      }
      setItems(Array.isArray(data) ? data : [])
      setError(null)
      try {
        const { data: treeData } = await apiClient.GET("/runs/{id}/tree", {
          params: { path: { id: runId } },
        })
        const nodes = (treeData as { nodes?: Array<{ id: string; title: string }> } | undefined)?.nodes
        if (Array.isArray(nodes)) {
          const titles: Record<string, string> = {}
          for (const node of nodes) {
            if (node?.id) titles[node.id] = node.title
          }
          setNodeTitles(titles)
        }
      } catch {
        // Названия необязательны: конфликт показываем и без них.
      }
    } catch {
      setItems([])
      setError("Сервер недоступен: не удалось загрузить конфликты.")
    } finally {
      setLoading(false)
    }
  }, [runId])

  useEffect(() => {
    // Загрузка асинхронная; состояние обновится после ответа API.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load()
  }, [load])

  if (loading) {
    return (
      <div className="space-y-2">
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-24 w-full" />
      </div>
    )
  }

  if (error) {
    return (
      <div className="space-y-3 rounded-xl border border-destructive/50 p-8 text-center">
        <p className="text-sm text-destructive">{error}</p>
        <Button variant="outline" onClick={() => void load()}>
          <RefreshCwIcon className="size-4" />
          Повторить
        </Button>
      </div>
    )
  }

  if (items.length === 0) {
    return (
      <div className="flex flex-col items-center gap-3 rounded-xl border border-dashed p-10 text-center">
        <div className="flex size-10 items-center justify-center rounded-full bg-muted">
          <ScaleIcon className="size-5 text-muted-foreground" />
        </div>
        <div className="space-y-1">
          <p className="font-medium">Конфликтов не найдено</p>
          <p className="text-sm text-muted-foreground">
            Источники не противоречили друг другу - синтезатору не пришлось выбирать победителя.
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-3">
      <p className="text-sm text-muted-foreground">
        Найдено конфликтов: {items.length}. Предварительный вариант выбран по приоритету источника и требует проверки:
        интервью → профстандарт → вакансия → документ.
      </p>
      {items.map((conflict) => (
        <div key={conflict.id} className="space-y-3 rounded-xl border p-4">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="secondary">{FIELD_LABELS[conflict.field] ?? conflict.field}</Badge>
            <span className="text-xs text-muted-foreground">
              узел «{nodeTitles[conflict.node_id] ?? "не найден в дереве"}»
            </span>
            {onFocusNode && (
              <Button
                variant="ghost"
                size="sm"
                className="ml-auto h-6 px-2 text-xs"
                onClick={() => onFocusNode(conflict.node_id)}
              >
                Показать в дереве
              </Button>
            )}
          </div>

          <ul className="space-y-2">
            {conflict.values.map((value, index) => {
              const winner = value.source_id === conflict.winner.source_id && value.value === conflict.winner.value
              return (
                <li
                  key={`${value.source_id}-${index}`}
                  className={`flex flex-wrap items-center gap-2 rounded-lg border px-3 py-2 text-sm ${
                    winner ? "border-success/40 bg-success/10" : "bg-muted/30"
                  }`}
                >
                  <Badge variant="outline">
                    {SOURCE_TYPE_LABEL[value.source_type] ?? value.source_type}
                  </Badge>
                  <span className="min-w-0 flex-1 break-words">{value.value}</span>
                  <span className="text-xs text-muted-foreground">приоритет {value.priority}</span>
                  {winner && <span className="text-xs font-medium text-warning">Предварительно выбрано - требуется проверка</span>}
                </li>
              )
            })}
          </ul>

          <p className="text-xs text-muted-foreground">{conflict.reason}</p>
        </div>
      ))}
    </div>
  )
}
