/**
 * @file NewResearch.tsx
 * @description Запуск исследования профессиональной деятельности.
 */

import { useState, type FormEvent } from "react"
import { useNavigate } from "react-router-dom"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { ApiRequestError, apiClient } from "@/lib/api/client"
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
  { icon: FileSearchIcon, title: "Анализ данных", description: "Анализируются вакансии, профстандарты и методические материалы." },
  {
    icon: NetworkIcon,
    title: "Синтез дерева",
    description: "Строится дерево деятельности с функциями, задачами и действиями специалиста.",
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
  const [apiError, setApiError] = useState<string | null>(null)

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

  const canSubmit = !professionError && !submitting

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setSubmitted(true)
    setApiError(null)
    if (!canSubmit) return
    setSubmitting(true)
    try {
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
      const description = error instanceof ApiRequestError ? error.message : "Сервер недоступен. Попробуйте ещё раз."
      setApiError(description)
      toast.error("Не удалось запустить исследование", { description })
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="space-y-6">
      <div className="grid items-start gap-6 lg:grid-cols-[1fr_360px]">
        <Card className="gap-4 overflow-visible p-5" style={{ overflow: "visible" }}>
          <CardHeader>
            <CardTitle>Новое исследование</CardTitle>
            <CardDescription>Укажите роль, по которой нужно провести интервью и построить дерево деятельности.</CardDescription>
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

              {apiError && (
                <p className="flex items-center gap-1.5 text-sm text-destructive">
                  <AlertCircleIcon className="size-3.5 shrink-0" />
                  {apiError}
                </p>
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

    </div>
  )
}
