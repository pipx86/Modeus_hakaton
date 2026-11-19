/**
 * @file NewResearch.tsx
 * @description Создание исследования: выбор роли с подсказками, запуск интервью
 * или подбор плана обучения для новичка.
 */

import { useEffect, useState, type FormEvent } from "react"
import { useNavigate } from "react-router-dom"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  ApiRequestError,
  apiClient,
  type RunEstimate,
} from "@/lib/api/client"
import { stripControlChars } from "@/lib/security"
import {
  AlertCircleIcon,
  BookOpenIcon,
  FileSearchIcon,
  Loader2Icon,
  MessageSquareTextIcon,
  NetworkIcon,
  SparklesIcon,
} from "lucide-react"

const STEPS = [
  { icon: MessageSquareTextIcon, title: "Интервью", description: "Собирается информация о реальной деятельности специалиста." },
  { icon: FileSearchIcon, title: "Анализ данных", description: "Анализируются вакансии, профстандарты и методические материалы." },
  {
    icon: NetworkIcon,
    title: "Синтез дерева",
    description: "Строится дерево функции, задачи и действия по собранным данным.",
  },
]

const LEARNING_STEPS = [
  {
    icon: BookOpenIcon,
    title: "Выбор профессии",
    description: "Вы указываете роль, на которую хотите обучиться.",
  },
  {
    icon: FileSearchIcon,
    title: "Анализ рынка",
    description: "Система собирает требования из актуальных вакансий и материалов.",
  },
  {
    icon: SparklesIcon,
    title: "План обучения",
    description: "Получаете пошаговый план: что изучать, что практиковать и в каком порядке.",
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
  const [runType, setRunType] = useState<"learning" | "chat">("chat")
  const [apiError, setApiError] = useState<string | null>(null)
  const [networkError, setNetworkError] = useState<string | null>(null)

  const [roleOpen, setRoleOpen] = useState(false)
  const [selectedLevel, setSelectedLevel] = useState<string | null>(null)
  const learningMode = runType === "learning"

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

  const canSubmit = !professionError && !submitting

  useEffect(() => {
    if (learningMode) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setEstimate(null)
      return
    }
    const value = profession.trim()
    if (value.length < 2) {
      setEstimate(null)
      return
    }
    let cancelled = false
    const timer = window.setTimeout(() => {
      void (async () => {
        try {
          const { data, error } = await apiClient.GET("/runs/estimate", {
            params: { query: { profession: value, type: "chat" } },
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
  }, [profession, learningMode])

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setSubmitted(true)
    setApiError(null)
    setNetworkError(null)
    if (!canSubmit) return
    setSubmitting(true)
    try {
      if (learningMode) {
        const { data: run, error: runError } = await apiClient.POST("/runs", {
          body: { profession: stripControlChars(profession).trim(), type: "learning" },
        })
        if (runError) {
          setApiError(runError.message)
          toast.error("Не удалось запустить подбор плана", { description: runError.message })
          return
        }
        toast.success("Подбор плана запущен")
        navigate(run?.id ? `/research/${run.id}` : "/")
        return
      }
      const { data: run, error: runError } = await apiClient.POST("/runs", {
        body: { profession: stripControlChars(profession).trim(), type: "chat" },
      })
      if (runError) {
        setApiError(runError.message)
        toast.error("Не удалось запустить исследование", { description: runError.message })
        return
      }
      toast.success("Исследование запущено")
      navigate(run?.id ? `/research/${run.id}` : "/")
    } catch (error) {
      const description =
        error instanceof ApiRequestError
          ? error.message
          : learningMode
            ? "Не удалось составить план обучения. Попробуйте ещё раз."
            : "Проверьте, что бэкенд или мок запущен (npm run mock)."
      if (learningMode) setApiError(description)
      else
        setNetworkError(
          "Сервер недоступен. Проверьте, что бэкенд или мок запущен (npm run mock).",
        )
      toast.error(learningMode ? "План не готов" : "Сервер недоступен", { description })
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="space-y-6">
      <div className="grid items-start gap-6 lg:grid-cols-[1fr_360px]">
        <Card className="gap-4 overflow-visible p-5" style={{ overflow: "visible" }}>
          <CardHeader>
            <CardTitle>
              {learningMode ? "План обучения по профессии" : "Новое исследование"}
            </CardTitle>
            <CardDescription>
              {learningMode
                ? "Выберите профессию - соберём требования рынка и составим план обучения с нуля."
                : "Укажите роль - агенты проведут интервью и построят дерево деятельности."}
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
                <Label>Что нужно сделать</Label>
                <div className="flex flex-wrap gap-2">
                  <Button
                    type="button"
                    size="sm"
                    variant={!learningMode ? "secondary" : "outline"}
                    aria-pressed={!learningMode}
                    onClick={() => {
                      setRunType("chat")
                      setApiError(null)
                    }}
                  >
                    <MessageSquareTextIcon className="mr-1.5 size-3.5" />
                    Интервью с агентом
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant={learningMode ? "secondary" : "outline"}
                    aria-pressed={learningMode}
                    onClick={() => {
                      setRunType("learning")
                      setApiError(null)
                    }}
                  >
                    <BookOpenIcon className="mr-1.5 size-3.5" />
                    План обучения
                  </Button>
                </div>
                {learningMode && (
                  <p className="rounded-lg border bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
                    Подойдёт, если вы только выбираете профессию, не нужно ничего знать заранее.
                    План строится по актуальным вакансиям и требованиям рынка.
                  </p>
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

              {estimate && !learningMode && (
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
                    · по {estimate.sample_size} завершённым исследованиям
                  </p>
                </div>
              )}
              <Button type="submit" disabled={!canSubmit} className="w-full">
                {submitting && <Loader2Icon className="size-4 animate-spin" />}
                {submitting
                  ? learningMode
                    ? "Составляем план…"
                    : "Запуск…"
                  : learningMode
                    ? "Составить план обучения"
                    : "Начать исследование"}
              </Button>
            </form>
          </CardContent>
        </Card>

        <div className="space-y-4">
          <div className="rounded-xl border p-5">
            <h2 className="text-base font-semibold">Как это работает</h2>
            <p className="mt-1 text-sm text-muted-foreground">
              {learningMode ? "Три шага до плана обучения." : "Три этапа, которые проходят ваши данные."}
            </p>
          </div>
          <ol className="space-y-4">
            {(learningMode ? LEARNING_STEPS : STEPS).map((step, index) => {
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

    </div>
  )
}
