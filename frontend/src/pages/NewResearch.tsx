/**
 * @file NewResearch.tsx
 * @description Создание исследования: выбор роли с подсказками и запуск прогона.
 */

import { useEffect, useState, type FormEvent } from "react"
import { useNavigate } from "react-router-dom"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { apiClient, type RunEstimate } from "@/lib/api/client"
import { Textarea } from "@/components/ui/textarea"
import { stripControlChars } from "@/lib/security"
import {
  AlertCircleIcon,
  FileSearchIcon,
  Loader2Icon,
  MessageSquareTextIcon,
  NetworkIcon,
} from "lucide-react"

const STEPS = [
  { icon: MessageSquareTextIcon, title: "Интервью", description: "Собирается информация о реальной деятельности специалиста." },
  { icon: FileSearchIcon, title: "Анализ данных", description: "Анализируются вакансии, профстандарты и методички." },
  {
    icon: NetworkIcon,
    title: "Синтез дерева",
    description: "Строится дерево функции, задачи и действия по собранным данным.",
  },
]

interface RoleSuggestion {
  title: string
  levels?: string[]
}

const ROLE_SUGGESTIONS: RoleSuggestion[] = [
  { title: "Backend разработчик", levels: ["junior", "middle", "senior"] },
  { title: "Аналитик данных" },
  { title: "DevOps-инженер" },
]

const PROFESSION_MAX = 120

export function NewResearch() {
  const navigate = useNavigate()
  const [profession, setProfession] = useState("")
  const [submitted, setSubmitted] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [estimate, setEstimate] = useState<RunEstimate | null>(null)
  const [runType, setRunType] = useState<"chat" | "batch">("chat")
  const [transcript, setTranscript] = useState("")
  const [transcriptTouched, setTranscriptTouched] = useState(false)
  const [apiError, setApiError] = useState<string | null>(null)
  const [networkError, setNetworkError] = useState<string | null>(null)

  const [roleOpen, setRoleOpen] = useState(false)
  const [selectedLevel, setSelectedLevel] = useState<string | null>(null)

  const professionError =
    profession.trim().length === 0
      ? "Укажите профессию или роль"
      : profession.trim().length > PROFESSION_MAX
        ? `Не длиннее ${PROFESSION_MAX} символов`
        : null

  const filteredRoles = ROLE_SUGGESTIONS.filter((role) => {
    const query = profession.trim().toLowerCase()
    if (!query) return true
    if (role.title.toLowerCase().includes(query)) return true
    return (role.levels ?? []).some((level) => level.toLowerCase().includes(query))
  })

  const transcriptError =
    runType === "batch" && transcript.trim().length < 20
      ? "Вставьте транскрипт интервью: минимум 20 символов"
      : null
  const canSubmit = !professionError && !transcriptError && !submitting

  useEffect(() => {
    const value = profession.trim()
    if (value.length < 2) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setEstimate(null)
      return
    }
    let cancelled = false
    const timer = window.setTimeout(() => {
      void (async () => {
        try {
          const { data, error } = await apiClient.GET("/runs/estimate", {
            params: { query: { profession: value, type: runType } },
          })
          if (!cancelled) {
            setEstimate(
              !error && data && typeof data.estimated_tokens === "number" ? data : null,
            )
          }
        } catch {
          if (!cancelled) setEstimate(null)
        }
      })()
    }, 600)
    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [profession, runType])

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setSubmitted(true)
    setApiError(null)
    setNetworkError(null)
    if (!canSubmit) return
    setSubmitting(true)
    try {
      const { data: run, error: runError } = await apiClient.POST("/runs", {
        body:
          runType === "batch"
            ? {
                profession: stripControlChars(profession).trim(),
                type: "batch",
                transcript: stripControlChars(transcript).trim(),
              }
            : { profession: stripControlChars(profession).trim(), type: "chat" },
      })
      if (runError) {
        setApiError(runError.message)
        toast.error("Не удалось запустить исследование", { description: runError.message })
        return
      }
      toast.success("Исследование запущено")
      navigate(run?.id ? `/research/${run.id}` : "/")
    } catch {
      setNetworkError("Сервер недоступен. Проверьте, что бэкенд или мок запущен (npm run mock).")
      toast.error("Сервер недоступен", {
        description: "Проверьте, что бэкенд или мок запущен (npm run mock).",
      })
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="grid items-start gap-6 lg:grid-cols-[1fr_360px]">
      <Card className="gap-4 overflow-visible p-5" style={{ overflow: "visible" }}>
        <CardHeader>
          <CardTitle>Новое исследование</CardTitle>
          <CardDescription>
            Укажите роль - агенты проведут интервью и построят дерево деятельности.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="space-y-5">
            <div className="space-y-2">
              <Label htmlFor="profession">Профессия / роль</Label>
              <div className="relative">
                <Input
                  id="profession"
                  value={profession}
                  onChange={(event) => {
                    setProfession(event.target.value)
                    setSelectedLevel(null)
                    setRoleOpen(true)
                  }}
                  onFocus={() => setRoleOpen(true)}
                  onBlur={() =>
                    window.setTimeout(() => {
                      setRoleOpen(false)
                      setSelectedLevel(null)
                    }, 100)
                  }
                  placeholder="Например: Data Scientist"
                  autoFocus
                  maxLength={PROFESSION_MAX}
                  autoComplete="off"
                  role="combobox"
                  aria-expanded={roleOpen}
                  aria-controls="role-suggestions"
                />
                {roleOpen && filteredRoles.length > 0 && (
                  <ul
                    id="role-suggestions"
                    className="absolute z-20 mt-1 max-h-60 w-full overflow-y-auto overflow-x-hidden rounded-lg border bg-popover p-1 text-popover-foreground shadow-md"
                  >
                    {filteredRoles.map((role) => (
                      <li key={role.title} className="flex items-center gap-1.5">
                        <button
                          type="button"
                          className="flex min-w-0 flex-1 items-center rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-accent hover:text-accent-foreground"
                          onMouseDown={(event) => event.preventDefault()}
                          onClick={() => {
                            const [selectedRole, level] = (selectedLevel ?? "").split("|")
                            const picked = selectedRole === role.title ? level : null
                            setProfession(picked ? `${role.title} (${picked})` : role.title)
                            setSelectedLevel(null)
                            setRoleOpen(false)
                          }}
                        >
                          <span className="truncate">{role.title}</span>
                        </button>
                        {role.levels && (
                          <div
                            className="flex shrink-0 items-center gap-1"
                            aria-label={`Уровни: ${role.title}`}
                          >
                            {role.levels.map((level) => {
                              const key = `${role.title}|${level}`
                              const active = selectedLevel === key
                              return (
                                <button
                                  key={level}
                                  type="button"
                                  aria-pressed={active}
                                  className={`rounded-md border px-2 py-1 text-xs capitalize transition-colors ${
                                    active
                                      ? "border-primary bg-primary/15 text-primary"
                                      : "hover:bg-accent hover:text-accent-foreground"
                                  }`}
                                  onMouseDown={(event) => event.preventDefault()}
                                  onClick={() => setSelectedLevel(key)}
                                >
                                  {level}
                                </button>
                              )
                            })}
                          </div>
                        )}
                      </li>
                    ))}
                  </ul>
                )}
              </div>
              {(submitted || profession.length > PROFESSION_MAX) && professionError && (
                <p className="flex items-center gap-1.5 text-sm text-destructive">
                  <AlertCircleIcon className="size-3.5 shrink-0" />
                  {professionError}
                </p>
              )}
            </div>

            <div className="space-y-2">
              <Label>Формат запуска</Label>
              <div className="flex flex-wrap gap-2">
                <Button
                  type="button"
                  size="sm"
                  variant={runType === "chat" ? "secondary" : "outline"}
                  aria-pressed={runType === "chat"}
                  onClick={() => setRunType("chat")}
                >
                  Интервью с агентом
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant={runType === "batch" ? "secondary" : "outline"}
                  aria-pressed={runType === "batch"}
                  onClick={() => setRunType("batch")}
                >
                  Готовый транскрипт
                </Button>
              </div>
              {runType === "batch" && (
                <div className="space-y-1.5">
                  <Label htmlFor="transcript">Транскрипт интервью</Label>
                  <Textarea
                    id="transcript"
                    value={transcript}
                    onChange={(event) => {
                      setTranscript(event.target.value)
                      setTranscriptTouched(true)
                    }}
                    onBlur={() => setTranscriptTouched(true)}
                    rows={7}
                    placeholder="Вставьте вопросы и ответы интервью: каждая реплика с новой строки..."
                  />
                  <p className="text-xs text-muted-foreground">
                    Batch-прогон сразу передаёт транскрипт аналитику и синтезатору, без чата с
                    интервьюером.
                  </p>
                  {(transcriptTouched || submitted) && transcriptError && (
                    <p className="flex items-center gap-1.5 text-sm text-destructive">
                      <AlertCircleIcon className="size-3.5 shrink-0" />
                      {transcriptError}
                    </p>
                  )}
                </div>
              )}
            </div>

            {apiError && (
              <p className="flex items-center gap-1.5 text-sm text-destructive">
                <AlertCircleIcon className="size-3.5 shrink-0" />
                {apiError}
              </p>
            )}
            {networkError && (
              <p className="flex items-center gap-1.5 text-sm text-destructive">
                <AlertCircleIcon className="size-3.5 shrink-0" />
                {networkError}
              </p>
            )}

            {estimate && (
              <div className="space-y-1 rounded-lg border bg-muted/20 p-3 text-xs">
                <p className="font-medium text-foreground">Оценка до запуска</p>
                <p className="text-muted-foreground">
                  ~{estimate.estimated_tokens.toLocaleString("ru-RU")} токенов · ~
                  {Math.max(1, Math.round(estimate.estimated_duration_seconds / 60))} мин ·{" "}
                  {estimate.estimated_cost_usd === null
                    ? "стоимость неизвестна"
                    : `$${estimate.estimated_cost_usd.toLocaleString("ru-RU", {
                        maximumFractionDigits: 4,
                      })}`}
                </p>
                <p className="text-muted-foreground">
                  Уверенность:{" "}
                  {estimate.confidence === "high"
                    ? "высокая"
                    : estimate.confidence === "medium"
                      ? "средняя"
                      : "низкая"}{" "}
                  · по {estimate.sample_size} завершённым прогонам
                </p>
              </div>
            )}
            <Button type="submit" disabled={!canSubmit} className="w-full">
              {submitting && <Loader2Icon className="size-4 animate-spin" />}
              {submitting ? "Запуск…" : "Начать исследование"}
            </Button>
          </form>
        </CardContent>
      </Card>

      <div className="space-y-4">
        <div className="rounded-xl border p-5">
          <h2 className="text-base font-semibold">Как это работает</h2>
          <p className="mt-1 text-sm text-muted-foreground">Три этапа, которые проходят ваши данные.</p>
        </div>
        <ol className="space-y-4">
          {STEPS.map((step, index) => {
            const Icon = step.icon
            return (
              <li key={step.title} className="flex gap-4 rounded-xl border p-5">
                <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/10">
                  <Icon className="size-4 text-primary" />
                </div>
                <div className="space-y-1">
                  <p className="text-sm font-medium">
                    <span className="mr-1.5 text-xs text-muted-foreground">{index + 1}.</span>
                    {step.title}
                  </p>
                  <p className="text-sm text-muted-foreground">{step.description}</p>
                </div>
              </li>
            )
          })}
        </ol>
      </div>
    </div>
  )
}