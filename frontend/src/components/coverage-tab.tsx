/**
 * @file coverage-tab.tsx
 * @description Матрица покрытия «функции × типы источников» из GET /runs/{id}/coverage.
 */

import { useEffect, useState } from "react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import {
  apiBaseUrl,
  apiClient,
  type CoverageResponse,
} from "@/lib/api/client"
import { getValidToken } from "@/lib/auth"
import { downloadText } from "@/lib/export-tree"
import { SOURCE_TYPE_LABEL } from "@/lib/status"
import { NODE_TYPE_LABELS } from "@/components/activity-tree-model"
import { DownloadIcon, Loader2Icon } from "lucide-react"
import { normalizeText } from "@/lib/text"

export function CoverageTab({ runId }: { runId: string }) {
  const [coverage, setCoverage] = useState<CoverageResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [retry, setRetry] = useState(0)
  const [downloading, setDownloading] = useState(false)

  useEffect(() => {
    let cancelled = false
    void (async () => {
      setLoading(true)
      try {
        const { data, error: requestError } = await apiClient.GET("/runs/{id}/coverage", {
          params: { path: { id: runId } },
        })
        if (cancelled) return
        if (requestError) throw new Error(requestError.message)
        if (!data || typeof data === "string") throw new Error("Пустой ответ покрытия.")
        setCoverage(data)
        setError(null)
      } catch (cause) {
        if (!cancelled) {
          setCoverage(null)
          setError(cause instanceof Error ? cause.message : "Не удалось загрузить покрытие.")
        }
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [runId, retry])

  const downloadCsv = async () => {
    setDownloading(true)
    try {
      const token = getValidToken()
      const response = await fetch(
        `${apiBaseUrl}/runs/${encodeURIComponent(runId)}/coverage?format=csv`,
        { headers: token ? { Authorization: `Bearer ${token}` } : undefined },
      )
      if (!response.ok) {
        toast.error("Не удалось скачать CSV", { description: `HTTP ${response.status}` })
        return
      }
      downloadText("coverage.csv", normalizeText(await response.text()), "text/csv;charset=utf-8")
    } catch {
      toast.error("Сервер недоступен", { description: "CSV не скачан." })
    } finally {
      setDownloading(false)
    }
  }

  if (loading) {
    return (
      <div className="flex items-center gap-2 rounded-xl border p-6 text-sm text-muted-foreground">
        <Loader2Icon className="size-4 animate-spin" />
        Загрузка матрицы покрытия…
      </div>
    )
  }

  if (error) {
    return (
      <div className="space-y-3 rounded-xl border border-destructive/50 p-4 text-sm">
        <p className="text-destructive">{error}</p>
        <Button variant="outline" size="sm" onClick={() => setRetry((value) => value + 1)}>
          Повторить
        </Button>
      </div>
    )
  }

  if (!coverage || coverage.rows.length === 0) {
    return (
      <p className="rounded-xl border border-dashed p-6 text-sm text-muted-foreground">
        Матрица пуста: в дереве нет ролей, функций или задач с обоснованиями.
      </p>
    )
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-xs text-muted-foreground">
          Строки - узлы дерева, столбцы - типы источников. Нули показывают пробелы в
          подтверждениях.
        </p>
        <Button
          variant="outline"
          size="sm"
          className="h-7 gap-1 px-2.5 text-xs"
          onClick={() => void downloadCsv()}
          disabled={downloading}
        >
          {downloading ? (
            <Loader2Icon className="size-3.5 animate-spin" />
          ) : (
            <DownloadIcon className="size-3.5" />
          )}
          Скачать CSV
        </Button>
      </div>
      <div className="overflow-x-auto rounded-xl border">
        <table className="w-full text-sm">
          <thead className="bg-muted/40 text-left text-xs text-muted-foreground">
            <tr>
              <th className="px-3 py-2 font-medium">Узел</th>
              {coverage.columns.map((column) => (
                <th key={column} className="px-3 py-2 text-center font-medium">
                  {SOURCE_TYPE_LABEL[column] ?? column}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {coverage.rows.map((row) => (
              <tr key={row.node_id} className="border-t">
                <td className="px-3 py-2">
                  <span className="font-medium">{row.title}</span>
                  <span className="ml-2 text-[11px] text-muted-foreground">
                    {NODE_TYPE_LABELS[row.type] ?? row.type}
                  </span>
                </td>
                {coverage.columns.map((column) => {
                  const value = row.cells[column] ?? 0
                  return (
                    <td
                      key={column}
                      className={`px-3 py-2 text-center ${
                        value === 0 ? "text-muted-foreground/40" : ""
                      }`}
                    >
                      {value}
                    </td>
                  )
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
