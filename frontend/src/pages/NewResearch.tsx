/**
 * @file NewResearch.tsx
 * @description Запуск исследования профессиональной деятельности.
 */

import { useCallback, useEffect, useState, type FormEvent } from "react"
import { useNavigate } from "react-router-dom"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  ApiRequestError,
  createRun,
  importHHProfile,
  importResume,
  listAcademicPrograms,
  type AcademicProgramOption,
  type CreateRunInput,
  type RunResultKind,
} from "@/lib/api/client"
import { stripControlChars } from "@/lib/security"
import {
  AlertCircleIcon,
  FileSearchIcon,
  Loader2Icon,
  MessageSquareTextIcon,
  NetworkIcon,
  RefreshCwIcon,
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

/** Варианты ожидаемого результата исследования (карточка 25). */
const RESULT_KIND_OPTIONS: { value: RunResultKind; label: string; description: string }[] = [
  {
    value: "activity",
    label: "Описание деятельности",
    description: "Интервью и дерево деятельности без привязки к программе.",
  },
  {
    value: "program",
    label: "Описание и сопоставление с образовательной программой",
    description: "Дополнительно свяжем узлы деятельности с элементами программы.",
  },
]

/** Подпись программы в выпадающем списке: код (если есть) и название. */
function programLabel(program: AcademicProgramOption): string {
  return program.code ? `${program.code} · ${program.title}` : program.title
}

/** Понятная ошибка загрузки списка программ. */
function describeProgramsError(error: unknown): string {
  if (error instanceof ApiRequestError) {
    if (error.status === 403) return "Список программ доступен методисту."
    if (error.status === 501) return "Список образовательных программ пока не подключён."
    if (error.status === 0) return "Сервер недоступен: список программ не загружен."
    return error.message
  }
  return "Не удалось загрузить список образовательных программ."
}

export function NewResearch() {
  const navigate = useNavigate()
  const [profession, setProfession] = useState("")
  const [githubUsername, setGithubUsername] = useState("")
  const [githubConsent, setGithubConsent] = useState(true)
  const [githubExclude, setGithubExclude] = useState("")
  const [profileContext, setProfileContext] = useState("")
  const [profileLabel, setProfileLabel] = useState("")
  const [hhURL, setHhURL] = useState("")
  const [profileLoading, setProfileLoading] = useState(false)
  const [submitted, setSubmitted] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [apiError, setApiError] = useState<string | null>(null)

  const [roleOpen, setRoleOpen] = useState(false)
  const [selectedLevel, setSelectedLevel] = useState<string | null>(null)

  // Карточка 25: ожидаемый результат. Программы грузятся лениво, только когда
  // пользователь выбрал режим сопоставления.
  const [resultKind, setResultKind] = useState<RunResultKind>("activity")
  const [programId, setProgramId] = useState<string | null>(null)
  const [programs, setPrograms] = useState<AcademicProgramOption[] | null>(null)
  const [programsRequested, setProgramsRequested] = useState(false)
  const [programsLoading, setProgramsLoading] = useState(false)
  const [programsError, setProgramsError] = useState<string | null>(null)

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

  const canSubmit = !professionError && !submitting && !(resultKind === "program" && !programId)
  const programsPending = programsLoading || (programs === null && !programsError)

  /** Загружает (или перезагружает) список программ; повторный вызов доступен из ошибки. */
  const loadPrograms = useCallback(async () => {
    setProgramsRequested(true)
    setProgramsLoading(true)
    setProgramsError(null)
    try {
      const options = await listAcademicPrograms()
      setPrograms(options)
      setProgramId((previous) =>
        previous && options.some((program) => program.id === previous) ? previous : null,
      )
    } catch (cause) {
      setPrograms([])
      setProgramId(null)
      setProgramsError(describeProgramsError(cause))
    } finally {
      setProgramsLoading(false)
    }
  }, [])

  useEffect(() => {
    if (resultKind !== "program" || programsRequested) return
    // Загрузка асинхронная: список появится после ответа API.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void loadPrograms()
  }, [resultKind, programsRequested, loadPrograms])

  const uploadResume = async (file: File | undefined) => {
    if (!file) return
    setProfileLoading(true)
    try {
      const imported = await importResume(file)
      setProfileContext(imported.text)
      setProfileLabel(imported.filename ? `Резюме: ${imported.filename}` : "Резюме загружено")
      toast.success("Резюме загружено")
    } catch (cause) {
      toast.error("Не удалось прочитать резюме", {
        description: cause instanceof Error ? cause.message : "Проверьте формат PDF/DOCX.",
      })
    } finally {
      setProfileLoading(false)
    }
  }

  const loadHHProfile = async () => {
    if (!hhURL.trim()) return
    setProfileLoading(true)
    try {
      const imported = await importHHProfile(hhURL.trim())
      setProfileContext(imported.text)
      setProfileLabel(imported.title ? `HH: ${imported.title}` : "Профиль HH загружен")
      toast.success("Профиль HH загружен")
    } catch (cause) {
      toast.error("Не удалось загрузить профиль HH", {
        description: cause instanceof Error ? cause.message : "Проверьте ссылку на резюме.",
      })
    } finally {
      setProfileLoading(false)
    }
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setSubmitted(true)
    setApiError(null)
    if (!canSubmit) return
    setSubmitting(true)
    try {
      const input: CreateRunInput = {
        profession: stripControlChars(profession).trim(),
        type: "chat",
        github_username: stripControlChars(githubUsername).trim() || undefined,
        github_consent: githubConsent,
        github_exclude: githubExclude.split(",").map((item) => item.trim()).filter(Boolean),
        profile_context: profileContext || undefined,
        result_kind: resultKind,
      }
      // program_id отправляем только для режима сопоставления с программой.
      if (resultKind === "program" && programId) input.program_id = programId
      const run = await createRun(input)
      toast.success("Исследование запущено")
      navigate(run?.id ? `/research/${run.id}` : "/")
    } catch (error) {
      const description =
        error instanceof ApiRequestError
          ? error.status === 0
            ? "Сервер недоступен. Попробуйте ещё раз."
            : error.message
          : "Сервер недоступен. Попробуйте ещё раз."
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

              <fieldset className="space-y-2">
                <legend className="mb-2 text-sm font-medium">Ожидаемый результат</legend>
                <div className="grid gap-2 sm:grid-cols-2">
                  {RESULT_KIND_OPTIONS.map((option) => {
                    const active = resultKind === option.value
                    return (
                      <label
                        key={option.value}
                        className={`flex cursor-pointer items-start gap-2 rounded-lg border p-3 text-sm transition-colors ${
                          active
                            ? "border-primary bg-primary/5"
                            : "hover:bg-accent/40 hover:text-accent-foreground"
                        }`}
                      >
                        <input
                          type="radio"
                          name="result-kind"
                          value={option.value}
                          checked={active}
                          onChange={() => setResultKind(option.value)}
                          disabled={submitting}
                          className="mt-0.5 size-3.5 shrink-0 accent-primary"
                        />
                        <span className="space-y-0.5">
                          <span className="block font-medium">{option.label}</span>
                          <span className="block text-xs text-muted-foreground">
                            {option.description}
                          </span>
                        </span>
                      </label>
                    )
                  })}
                </div>
              </fieldset>

              {resultKind === "program" && (
                <div className="space-y-2">
                  <Label htmlFor="program">Образовательная программа</Label>
                  {programsPending ? (
                    <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
                      <Loader2Icon className="size-3.5 animate-spin" />
                      Загружаем список программ…
                    </p>
                  ) : programsError ? (
                    <div className="space-y-2 rounded-lg border border-destructive/40 p-3 text-sm">
                      <p className="text-destructive">{programsError}</p>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => void loadPrograms()}
                        disabled={programsLoading}
                      >
                        <RefreshCwIcon className="size-3.5" />
                        Повторить
                      </Button>
                    </div>
                  ) : programs && programs.length > 0 ? (
                    <Select
                      value={programId ?? ""}
                      onValueChange={(value) => setProgramId(value || null)}
                      disabled={submitting}
                    >
                      <SelectTrigger id="program">
                        <SelectValue placeholder="Выберите программу" />
                      </SelectTrigger>
                      <SelectContent>
                        {programs.map((program) => (
                          <SelectItem key={program.id} value={program.id}>
                            {programLabel(program)}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  ) : (
                    <div className="space-y-2">
                      <p className="text-sm text-muted-foreground">
                        Список образовательных программ пуст.
                      </p>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => void loadPrograms()}
                        disabled={programsLoading}
                      >
                        <RefreshCwIcon className="size-3.5" />
                        Обновить список
                      </Button>
                    </div>
                  )}
                  {!programsPending && !programsError && !programId && (
                    <p className="text-xs text-muted-foreground">
                      Чтобы запустить исследование, выберите образовательную программу.
                    </p>
                  )}
                </div>
              )}

              <div className="space-y-2">
                <Label htmlFor="github-username">GitHub username (необязательно)</Label>
                <Input
                  id="github-username"
                  value={githubUsername}
                  onChange={(event) => setGithubUsername(event.target.value)}
                  placeholder="например, octocat"
                  autoComplete="off"
                  disabled={submitting}
                />
                <p className="text-xs text-muted-foreground">
                  Публичные проекты помогут агенту точнее спросить о навыках. Данные используются только для интервью.
                </p>
                {githubUsername.trim() && (
                  <label className="flex items-start gap-2 text-xs text-muted-foreground">
                    <input
                      type="checkbox"
                      className="mt-0.5 size-3.5 accent-primary"
                      checked={githubConsent}
                      onChange={(event) => setGithubConsent(event.target.checked)}
                      disabled={submitting}
                    />
                    <span>Разрешаю использовать публичный GitHub-профиль для этого интервью.</span>
                  </label>
                )}
                {githubUsername.trim() && (
                  <Input
                    value={githubExclude}
                    onChange={(event) => setGithubExclude(event.target.value)}
                    placeholder="Исключить репозитории через запятую"
                    disabled={submitting}
                    aria-label="Исключить репозитории GitHub"
                  />
                )}
              </div>

              <div className="space-y-2">
                <Label htmlFor="resume-file">Резюме PDF/DOCX или профиль HH (необязательно)</Label>
                <div className="flex flex-wrap items-center gap-2">
                  <Input
                    id="resume-file"
                    type="file"
                    accept=".pdf,.docx,.txt,.md"
                    className="max-w-xs"
                    onChange={(event) => void uploadResume(event.target.files?.[0])}
                    disabled={submitting || profileLoading}
                  />
                  <span className="text-xs text-muted-foreground">или</span>
                  <Input
                    value={hhURL}
                    onChange={(event) => setHhURL(event.target.value)}
                    placeholder="https://hh.ru/resume/..."
                    className="max-w-xs"
                    disabled={submitting || profileLoading}
                    aria-label="Ссылка на резюме HH"
                  />
                  <Button type="button" variant="outline" onClick={() => void loadHHProfile()} disabled={!hhURL.trim() || profileLoading}>
                    {profileLoading ? "Загружаем..." : "Загрузить HH"}
                  </Button>
                </div>
                {profileLabel && <p className="text-xs text-muted-foreground">{profileLabel}</p>}
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
