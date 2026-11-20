/**
 * @file CompareRuns.tsx
 * @description Страница сравнения двух завершённых прогонов одной профессии.
 */

import { useEffect, useState } from "react"
import { Link } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { apiClient, type ActivityNode, type Run, type RunCompare } from "@/lib/api/client"
import { NODE_TYPE_LABELS } from "@/components/activity-tree-model"
import { downloadText } from "@/lib/export-tree"
import { AlertTriangleIcon, ArrowLeftRightIcon, DownloadIcon, GitCompareIcon } from "lucide-react"

export function CompareRuns() {
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [baseId, setBaseId] = useState<string | null>(null)
  const [headId, setHeadId] = useState<string | null>(null)
  const [diff, setDiff] = useState<RunCompare | null>(null)
  const [compareError, setCompareError] = useState<string | null>(null)
  const [comparing, setComparing] = useState(false)

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const { data } = await apiClient.GET("/runs")
        if (cancelled) return
        const done = (data ?? []).filter((run) => run.status === "done")
        setRuns(done)
        setError(null)
      } catch {
        if (!cancelled) setError("Не удалось загрузить список исследований.")
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    if (!baseId || !headId || baseId === headId) return
    let cancelled = false
    void (async () => {
      await Promise.resolve()
      if (cancelled) return
      setComparing(true)
      try {
        const { data, error: requestError } = await apiClient.GET("/runs/{id}/compare", {
          params: { path: { id: baseId }, query: { against: headId } },
        })
        if (cancelled) return
        if (requestError) {
          setCompareError(requestError.message)
          setDiff(null)
          return
        }
        setDiff(data ?? null)
        setCompareError(null)
      } catch {
        if (!cancelled) setCompareError("Не удалось сравнить исследования.")
      } finally {
        if (!cancelled) setComparing(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [baseId, headId])

  return (
    <div className="space-y-6">
      <div className="space-y-1">
        <h1 className="text-3xl font-semibold tracking-tight">Сравнение исследований</h1>
        <p className="text-sm text-muted-foreground">
          Выберите два завершённых исследования по одной профессии, чтобы увидеть, что изменилось в дереве.
        </p>
      </div>

      {error ? (
        <div className="rounded-xl border border-destructive/50 p-6 text-center text-sm text-destructive">
          {error}
        </div>
      ) : loading ? (
        <div className="text-sm text-muted-foreground">Загрузка исследований…</div>
      ) : runs.length < 2 ? (
        <div className="flex flex-col items-center gap-3 rounded-xl border border-dashed p-10 text-center">
          <GitCompareIcon className="size-6 text-muted-foreground" />
          <p className="text-sm text-muted-foreground">
            Для сравнения нужно минимум два завершённых исследования. Сейчас доступно: {runs.length}.
          </p>
          <Button asChild variant="outline">
            <Link to="/">К исследованиям</Link>
          </Button>
        </div>
      ) : (
        <>
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Выберите версии</CardTitle>
              <CardDescription>Сравниваем дерево базового исследования с новым.</CardDescription>
            </CardHeader>
            <CardContent className="grid gap-3 sm:grid-cols-[1fr_auto_1fr] sm:items-end">
              <div className="space-y-2">
                <label className="text-sm font-medium" htmlFor="compare-base">
                  Базовое исследование
                </label>
                <Select value={baseId ?? undefined} onValueChange={setBaseId}>
                  <SelectTrigger id="compare-base" aria-label="Базовое исследование">
                    <SelectValue placeholder="Выберите исследование" />
                  </SelectTrigger>
                  <SelectContent>
                    {runs.map((run) => (
                      <SelectItem key={run.id} value={run.id}>
                        {run.profession}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <ArrowLeftRightIcon className="mx-auto hidden size-4 text-muted-foreground sm:block" />
              <div className="space-y-2">
                <label className="text-sm font-medium" htmlFor="compare-head">
                  Новое исследование
                </label>
                <Select value={headId ?? undefined} onValueChange={setHeadId}>
                  <SelectTrigger id="compare-head" aria-label="Новое исследование">
                    <SelectValue placeholder="Выберите исследование" />
                  </SelectTrigger>
                  <SelectContent>
                    {runs.map((run) => (
                      <SelectItem key={run.id} value={run.id}>
                        {run.profession}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </CardContent>
          </Card>

          {compareError && (
            <div className="rounded-xl border border-destructive/50 p-4 text-sm text-destructive">
              {compareError}
            </div>
          )}

          {!baseId || !headId ? (
            <p className="text-sm text-muted-foreground">Выберите оба исследования, чтобы увидеть разницу.</p>
          ) : baseId === headId ? (
            <p className="text-sm text-muted-foreground">
              Выберите разные исследования: сравнивать исследование само с собой нечего.
            </p>
          ) : diff ? (
            <div className="space-y-4">
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant="outline" className="bg-success/15 text-success">
                  +{diff.summary.added} добавлено
                </Badge>
                <Badge variant="outline" className="bg-destructive/15 text-destructive">
                  -{diff.summary.removed} удалено
                </Badge>
                <Badge variant="outline" className="bg-warning/15 text-warning">
                  ~{diff.summary.changed} изменено
                </Badge>
                <Button
                  variant="outline"
                  size="sm"
                  className="ml-auto h-7 gap-1 px-2.5 text-xs"
                  onClick={() =>
                    downloadText(
                      "run-compare.json",
                      JSON.stringify(diff, null, 2),
                      "application/json;charset=utf-8",
                    )
                  }
                >
                  <DownloadIcon className="size-3.5" />
                  Экспорт JSON
                </Button>
              </div>
              {!diff.same_profession && (
                <p className="flex items-center gap-2 rounded-xl border border-warning/50 bg-warning/10 p-3 text-sm text-warning">
                  <AlertTriangleIcon className="size-4 shrink-0" />
                  Исследования относятся к разным профессиям: diff может быть неинформативным.
                </p>
              )}
              {diff.summary.added + diff.summary.removed + diff.summary.changed === 0 ? (
                <p className="rounded-xl border bg-muted/20 p-4 text-sm text-muted-foreground">
                  Деревья совпадают: различий не найдено.
                </p>
              ) : (
                <div className="space-y-3">
                  {diff.added.length > 0 && (
                    <DiffList title="Добавленные узлы" nodes={diff.added} tone="success" />
                  )}
                  {diff.removed.length > 0 && (
                    <DiffList title="Удалённые узлы" nodes={diff.removed} tone="destructive" />
                  )}
                  {diff.changed.length > 0 && (
                    <Card>
                      <CardHeader>
                        <CardTitle className="text-base">Изменённые узлы</CardTitle>
                      </CardHeader>
                      <CardContent>
                        <ul className="space-y-2 text-sm">
                          {diff.changed.map((change) => (
                            <li key={change.after.id} className="rounded-lg border p-3">
                              <p className="font-medium">
                                «{change.before.title}» → «{change.after.title}»
                              </p>
                              <p className="text-xs text-muted-foreground">
                                Поля: {change.fields.join(", ")}
                              </p>
                            </li>
                          ))}
                        </ul>
                      </CardContent>
                    </Card>
                  )}
                </div>
              )}
            </div>
          ) : comparing ? (
            <div className="text-sm text-muted-foreground">Сравнение…</div>
          ) : (
            <div className="text-sm text-muted-foreground">Выберите оба исследования, чтобы увидеть разницу.</div>
          )}
        </>
      )}
    </div>
  )
}

function DiffList({
  title,
  nodes,
  tone,
}: {
  title: string
  nodes: ActivityNode[]
  tone: "success" | "destructive"
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <span className={`size-2 rounded-full ${tone === "success" ? "bg-success" : "bg-destructive"}`} />
          {title}
        </CardTitle>
      </CardHeader>
      <CardContent>
        <ul className="space-y-1 text-sm">
          {nodes.map((node) => (
            <li key={node.id} className="flex items-center gap-2">
              <Badge variant="secondary">{NODE_TYPE_LABELS[node.type] ?? node.type}</Badge>
              <span>{node.title}</span>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}
