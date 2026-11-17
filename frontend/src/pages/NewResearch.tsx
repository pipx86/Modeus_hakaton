import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { useEffect, useState, type FormEvent } from "react"
import { useNavigate } from "react-router-dom"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { apiClient, type SourceType } from "@/lib/api/client"
import {
  AlertCircleIcon,
  FileSearchIcon,
  FileTextIcon,
  Loader2Icon,
  MessageSquareTextIcon,
  NetworkIcon,
  PlusIcon,
  XIcon,
} from "lucide-react"

const STEPS = [
  { icon: MessageSquareTextIcon, title: "Интервью", description: "Собирается информация о реальной деятельности специалиста." },
  { icon: FileSearchIcon, title: "Анализ данных", description: "Анализируются вакансии, профстандарты и методички." },
  { icon: NetworkIcon, title: "Синтез дерева", description: "Строится дерево «функции -> задачи -> действия» по собранным данным." },
]

const ROLE_SUGGESTIONS = [
  "Backend разработчик (junior)",
  "Backend разработчик (middle)",
  "Backend разработчик (senior)",
  "Data Analyst",
  "DevOps-инженер",
]

const SOURCE_TYPE_LABELS: Record<SourceType, string> = {
  vacancy: "Вакансия",
  interview: "Интервью",
  standard: "Профстандарт",
  document: "Документ",
}

const PROFESSION_MAX = 120
const TITLE_MAX = 200

interface SourceDraft {
  type: SourceType
  title: string
  content: string
}

export function NewResearch() {
  const navigate = useNavigate()
  const [profession, setProfession] = useState("")
  const [submitted, setSubmitted] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [apiError, setApiError] = useState<string | null>(null)
  const [networkError, setNetworkError] = useState<string | null>(null)

  const [existingCount, setExistingCount] = useState<number | null>(null)
  const [drafts, setDrafts] = useState<SourceDraft[]>([])
  const [qType, setQType] = useState<SourceType>("document")
  const [qTitle, setQTitle] = useState("")
  const [qContent, setQContent] = useState("")
  const [roleOpen, setRoleOpen] = useState(false)

  useEffect(() => {
    const load = async () => {
      try {
        const { data } = await apiClient.GET("/sources")
        setExistingCount(data?.length ?? 0)
      } catch {
        setExistingCount(null)
      }
    }
    void load()
  }, [])

  const professionError =
    profession.trim().length === 0
      ? "Укажите профессию или роль"
      : profession.trim().length > PROFESSION_MAX
        ? `Не длиннее ${PROFESSION_MAX} символов`
        : null

  const filteredRoles = ROLE_SUGGESTIONS.filter((role) =>
    role.toLowerCase().includes(profession.trim().toLowerCase()),
  )

  const qTitleTrimmed = qTitle.trim()
  const qTitleError =
    qTitleTrimmed.length === 0
      ? "Введите название источника"
      : qTitleTrimmed.length > TITLE_MAX
        ? `Не длиннее ${TITLE_MAX} символов`
        : null

  const canSubmit = !professionError && !submitting
  const canAddDraft = !qTitleError

  const addDraft = () => {
    if (!canAddDraft) return
    setDrafts((prev) => [...prev, { type: qType, title: qTitleTrimmed, content: qContent.trim() }])
    setQType("document")
    setQTitle("")
    setQContent("")
  }

  const removeDraft = (index: number) => {
    setDrafts((prev) => prev.filter((_, position) => position !== index))
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setSubmitted(true)
    setApiError(null)
    setNetworkError(null)
    if (!canSubmit) return
    setSubmitting(true)
    try {
      const results = await Promise.all(
        drafts.map((draft) =>
          apiClient.POST("/sources", {
            body: { type: draft.type, title: draft.title, content: draft.content || undefined },
          }),
        ),
      )
      const failed = results.find((result) => result.error)
      if (failed?.error) {
        setApiError(failed.error.message)
        return
      }
      const { data: run, error: runError } = await apiClient.POST("/runs", {
        body: { profession: profession.trim() },
      })
      if (runError) {
        setApiError(runError.message)
        return
      }
      toast.success("Исследование запущено")
      navigate(run?.id ? `/research/${run.id}` : "/")
    } catch {
      setNetworkError("Сервер недоступен. Проверьте, что бэкенд или мок запущен (npm run mock).")
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="grid gap-6 lg:grid-cols-[1fr_360px]">
      <Card className="gap-4 p-5">
        <CardHeader>
          <CardTitle>Новое исследование</CardTitle>
          <CardDescription>
            Выберите роль и при необходимости добавьте источники. Агенты запустят анализ по ним.
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
                    setRoleOpen(true)
                  }}
                  onFocus={() => setRoleOpen(true)}
                  onBlur={() => window.setTimeout(() => setRoleOpen(false), 100)}
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
                    className="absolute z-20 mt-1 max-h-60 w-full overflow-auto rounded-lg border bg-popover p-1 text-popover-foreground shadow-md"
                  >
                    {filteredRoles.map((role) => (
                      <li key={role}>
                        <button
                          type="button"
                          className="w-full rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-accent hover:text-accent-foreground"
                          onMouseDown={(event) => event.preventDefault()}
                          onClick={() => {
                            setProfession(role)
                            setRoleOpen(false)
                          }}
                        >
                          {role}
                        </button>
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

            <div className="space-y-3 rounded-xl border p-4">
              <div className="flex items-center justify-between gap-2">
                <p className="text-sm font-medium">Источники к прогону</p>
                <span className="text-xs text-muted-foreground">
                  {existingCount === null
                    ? "Сервер недоступен"
                    : `На сервере уже ${existingCount}. Агенты учтут их вместе с новыми`}
                </span>
              </div>

              {drafts.length > 0 && (
                <ul className="flex flex-wrap gap-2">
                  {drafts.map((draft, index) => (
                    <li
                      key={`${draft.title}-${index}`}
                      className="flex items-center gap-1.5 rounded-full border bg-muted/40 px-2.5 py-1 text-xs"
                    >
                      <FileTextIcon className="size-3 shrink-0 text-muted-foreground" />
                      <span className="max-w-40 truncate">{draft.title}</span>
                      <span className="text-muted-foreground">· {SOURCE_TYPE_LABELS[draft.type]}</span>
                      <button
                        type="button"
                        onClick={() => removeDraft(index)}
                        aria-label={`Удалить черновик ${draft.title}`}
                        className="text-muted-foreground transition-colors hover:text-foreground"
                      >
                        <XIcon className="size-3" />
                      </button>
                    </li>
                  ))}
                </ul>
              )}

              <div className="grid gap-3 sm:grid-cols-[160px_1fr]">
                <div className="space-y-1.5">
                  <Label htmlFor="quick-type">Тип</Label>
                  <Select value={qType} onValueChange={(value) => setQType(value as SourceType)}>
                    <SelectTrigger id="quick-type">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {(Object.keys(SOURCE_TYPE_LABELS) as SourceType[]).map((key) => (
                        <SelectItem key={key} value={key}>
                          {SOURCE_TYPE_LABELS[key]}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="quick-title">Название</Label>
                  <Input
                    id="quick-title"
                    value={qTitle}
                    onChange={(event) => setQTitle(event.target.value)}
                    placeholder="Вакансия"
                    maxLength={TITLE_MAX}
                  />
                  {qTitle.length > 0 && qTitleError && (
                    <p className="text-xs text-destructive">{qTitleError}</p>
                  )}
                </div>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="quick-content">Содержимое (необязательно)</Label>
                <Textarea
                  id="quick-content"
                  value={qContent}
                  onChange={(event) => setQContent(event.target.value)}
                  placeholder="Текст вакансии, профстандарта или расшифровка интервью…"
                  rows={3}
                />
              </div>
              <Button type="button" variant="outline" size="sm" onClick={addDraft} disabled={!canAddDraft}>
                <PlusIcon className="size-4" />
                Добавить источник
              </Button>
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