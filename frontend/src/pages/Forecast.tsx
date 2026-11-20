/**
 * @file Forecast.tsx
 * @description Тренды навыков, регрессия востребованности и сценарный прогноз дерева.
 */

import { useEffect, useMemo, useState } from "react"
import {
  AlertTriangleIcon,
  ArrowDownIcon,
  ArrowUpIcon,
  Loader2Icon,
  SparklesIcon,
  TrendingUpIcon,
} from "lucide-react"
import {
  apiClient,
  getSkillRegression,
  getSkillTrends,
  getTreeForecast,
  type RegressionResponse,
  type Run,
  type SkillTrendsResponse,
  type TreeForecastResponse,
} from "@/lib/api/client"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"

const TAB_TRIGGER_CLASS = "px-3 text-xs sm:text-sm"

function percent(value?: number): string {
  if (typeof value !== "number" || Number.isNaN(value)) return "-"
  return `${value > 0 ? "+" : ""}${value.toFixed(1)}%`
}

function TrendRows({ items }: { items: SkillTrendsResponse["forecasts"] }) {
  if (items.length === 0) {
    return <p className="text-sm text-muted-foreground">Нет накопленной истории навыков. Нужны месячные срезы вакансий.</p>
  }
  return (
    <div className="space-y-2">
      {items.map((item) => {
        const change = item.predicted_change_pct ?? 0
        return (
          <div key={item.skill} className="rounded-lg border px-3 py-2">
            <div className="flex items-center justify-between gap-2">
              <span className="font-medium">{item.skill}</span>
              <span className={change >= 0 ? "text-success" : "text-destructive"}>{percent(change)}</span>
            </div>
            <div className="mt-1 flex items-center justify-between text-xs text-muted-foreground">
              <span>частота: {item.current_frequency?.toFixed(1) ?? "-"}</span>
              <span>уверенность: {item.confidence !== undefined ? `${Math.round(item.confidence * 100)}%` : "-"}</span>
            </div>
            <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-muted">
              <div
                className={change >= 0 ? "h-full bg-success" : "h-full bg-destructive"}
                style={{ width: `${Math.min(100, Math.max(4, Math.abs(change) * 4))}%` }}
              />
            </div>
          </div>
        )
      })}
    </div>
  )
}

export function Forecast() {
  const [tab, setTab] = useState("trends")
  const [runs, setRuns] = useState<Run[]>([])
  const [profession, setProfession] = useState("")
  const [growing, setGrowing] = useState<SkillTrendsResponse | null>(null)
  const [declining, setDeclining] = useState<SkillTrendsResponse | null>(null)
  const [regression, setRegression] = useState<RegressionResponse | null>(null)
  const [tree, setTree] = useState<TreeForecastResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    void (async () => {
      const response = await apiClient.GET("/runs")
      const items = (response.data ?? []).filter((run) => run.status === "done")
      setRuns(items)
      if (!profession && items[0]?.profession) setProfession(items[0].profession)
    })()
  }, [profession])

  useEffect(() => {
    if (tab !== "trends") return
    let cancelled = false
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true)
    setError(null)
    void Promise.all([getSkillTrends("growing"), getSkillTrends("declining")])
      .then(([up, down]) => {
        if (cancelled) return
        setGrowing(up)
        setDeclining(down)
      })
      .catch((cause) => {
        if (!cancelled) setError(cause instanceof Error ? cause.message : "Не удалось загрузить тренды")
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [tab])

  useEffect(() => {
    if (tab !== "regression") return
    let cancelled = false
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true)
    setError(null)
    void getSkillRegression()
      .then((value) => {
        if (!cancelled) setRegression(value)
      })
      .catch((cause) => {
        if (!cancelled) setError(cause instanceof Error ? cause.message : "Не удалось загрузить регрессию")
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [tab])

  useEffect(() => {
    if (tab !== "tree" || !profession) return
    let cancelled = false
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true)
    setError(null)
    void getTreeForecast(profession)
      .then((value) => {
        if (!cancelled) setTree(value)
      })
      .catch((cause) => {
        if (!cancelled) {
          setTree(null)
          setError(cause instanceof Error ? cause.message : "Не удалось построить сценарий дерева")
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [tab, profession])

  const professions = useMemo(
    () => Array.from(new Set(runs.map((run) => run.profession).filter(Boolean))),
    [runs],
  )

  return (
    <div className="space-y-6">
      <div className="space-y-1">
        <h1 className="flex items-center gap-2 text-3xl font-semibold tracking-tight">
          <TrendingUpIcon className="size-7" />
          Прогноз рынка
        </h1>
        <p className="text-muted-foreground">Тренды навыков, объяснимая регрессия и сценарий изменения дерева.</p>
      </div>

      {error && (
        <div className="flex items-center gap-2 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <AlertTriangleIcon className="size-4" />
          {error}
        </div>
      )}

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="trends" className={TAB_TRIGGER_CLASS}>Тренды навыков</TabsTrigger>
          <TabsTrigger value="regression" className={TAB_TRIGGER_CLASS}>Регрессия</TabsTrigger>
          <TabsTrigger value="tree" className={TAB_TRIGGER_CLASS}>Дерево через год</TabsTrigger>
        </TabsList>

        <TabsContent value="trends" className="mt-4">
          {loading ? (
            <div className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2Icon className="size-4 animate-spin" /> Загружаем тренды...</div>
          ) : (
            <div className="grid gap-4 lg:grid-cols-2">
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2 text-base"><ArrowUpIcon className="size-4 text-success" /> Растущие навыки</CardTitle>
                  <CardDescription>По истории частоты навыков в вакансиях.</CardDescription>
                </CardHeader>
                <CardContent><TrendRows items={growing?.forecasts ?? []} /></CardContent>
              </Card>
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2 text-base"><ArrowDownIcon className="size-4 text-destructive" /> Снижающиеся навыки</CardTitle>
                  <CardDescription>Навыки, теряющие частоту.</CardDescription>
                </CardHeader>
                <CardContent><TrendRows items={declining?.forecasts ?? []} /></CardContent>
              </Card>
            </div>
          )}
        </TabsContent>

        <TabsContent value="regression" className="mt-4">
          {loading ? (
            <div className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2Icon className="size-4 animate-spin" /> Считаем модель...</div>
          ) : regression ? (
            <Card>
              <CardHeader>
                <CardTitle className="text-base">Что влияет на востребованность навыка</CardTitle>
                <CardDescription>
                  {regression.description} Выборка: {regression.samples} вакансий.
                </CardDescription>
              </CardHeader>
              <CardContent className="overflow-x-auto">
                {regression.skills.length === 0 ? (
                  <p className="text-sm text-muted-foreground">Недостаточно размеченных вакансий для модели.</p>
                ) : (
                  <table className="w-full text-left text-sm">
                    <thead className="text-xs text-muted-foreground">
                      <tr className="border-b">
                        <th className="py-2 pr-3 font-medium">Навык</th>
                        <th className="py-2 pr-3 font-medium">R²</th>
                        <th className="py-2 pr-3 font-medium">Опыт middle</th>
                        <th className="py-2 pr-3 font-medium">Опыт senior</th>
                        <th className="py-2 pr-3 font-medium">Зарплата</th>
                        <th className="py-2 pr-3 font-medium">Москва</th>
                      </tr>
                    </thead>
                    <tbody>
                      {regression.skills.map((item) => (
                        <tr key={item.skill} className="border-b last:border-b-0">
                          <td className="py-2 pr-3 font-medium">{item.skill}</td>
                          <td className="py-2 pr-3">{item.r2.toFixed(2)}</td>
                          {item.coefficients.map((coefficient) => (
                            <td key={coefficient.name} className={coefficient.estimate >= 0 ? "py-2 pr-3 text-success" : "py-2 pr-3 text-destructive"} title={`t = ${coefficient.t_stat.toFixed(2)}`}>
                              {coefficient.estimate >= 0 ? "+" : ""}{coefficient.estimate.toFixed(3)}
                            </td>
                          ))}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </CardContent>
            </Card>
          ) : null}
        </TabsContent>

        <TabsContent value="tree" className="mt-4 space-y-4">
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Профессия для сценария</CardTitle>
              <CardDescription>Берётся последнее завершённое исследование выбранной профессии.</CardDescription>
            </CardHeader>
            <CardContent>
              <Select value={profession} onValueChange={setProfession}>
                <SelectTrigger className="w-full max-w-md" aria-label="Профессия для прогноза дерева">
                  <SelectValue placeholder="Выберите профессию" />
                </SelectTrigger>
                <SelectContent>
                  {professions.map((value) => <SelectItem key={value} value={value}>{value}</SelectItem>)}
                </SelectContent>
              </Select>
            </CardContent>
          </Card>

          {loading ? (
            <div className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2Icon className="size-4 animate-spin" /> Строим сценарий...</div>
          ) : tree ? (
            <>
              <Card>
                <CardHeader>
                  <CardTitle className="flex flex-wrap items-center gap-2 text-base">
                    <SparklesIcon className="size-4" /> Сценарий на {tree.horizon_months} месяцев
                    <Badge variant={tree.scenario ? "secondary" : "default"}>{tree.scenario ? "сценарий" : "прогноз"}</Badge>
                  </CardTitle>
                  <CardDescription>{tree.summary}</CardDescription>
                </CardHeader>
                <CardContent className="grid gap-3 md:grid-cols-3">
                  {(["grow", "stable", "decline"] as const).map((status) => {
                    const count = tree.nodes.filter((node) => node.status === status).length
                    const label = status === "grow" ? "Вырастут" : status === "decline" ? "Сократятся" : "Останутся"
                    return (
                      <div key={status} className="rounded-lg border p-3">
                        <p className="text-xs text-muted-foreground">{label}</p>
                        <p className="text-2xl font-semibold">{count}</p>
                      </div>
                    )
                  })}
                </CardContent>
              </Card>
              <div className="grid gap-4 lg:grid-cols-2">
                <Card>
                  <CardHeader><CardTitle className="text-base">Узлы дерева</CardTitle></CardHeader>
                  <CardContent className="max-h-96 space-y-2 overflow-y-auto">
                    {tree.nodes.map((node) => (
                      <div key={node.id} className="flex items-start justify-between gap-3 rounded-lg border p-2 text-sm">
                        <div>
                          <p className="font-medium">{node.title}</p>
                          <p className="text-xs text-muted-foreground">{node.reason}</p>
                        </div>
                        <Badge variant={node.status === "grow" ? "default" : node.status === "decline" ? "destructive" : "secondary"}>
                          {node.status === "grow" ? "рост" : node.status === "decline" ? "спад" : "стабильно"}
                        </Badge>
                      </div>
                    ))}
                  </CardContent>
                </Card>
                <Card>
                  <CardHeader><CardTitle className="text-base">Новые действия</CardTitle><CardDescription>Навыки, которых пока нет в дереве.</CardDescription></CardHeader>
                  <CardContent className="space-y-2">
                    {tree.new_nodes.length === 0 ? (
                      <p className="text-sm text-muted-foreground">Новых направлений не найдено.</p>
                    ) : tree.new_nodes.map((node) => (
                      <div key={node.title} className="rounded-lg border p-2 text-sm">
                        <p className="font-medium">{node.title}</p>
                        <p className="text-xs text-muted-foreground">{node.reason}</p>
                      </div>
                    ))}
                  </CardContent>
                </Card>
              </div>
            </>
          ) : null}
        </TabsContent>
      </Tabs>
    </div>
  )
}
