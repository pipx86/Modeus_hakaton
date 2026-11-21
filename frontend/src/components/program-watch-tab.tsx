/**
 * @file program-watch-tab.tsx
 * @description Реестр источников образовательных программ: добавление ссылки со
 * строгим ключом программы, проверка по расписанию и вручную, включение и удаление.
 *
 * Данные: `GET/POST /admin/program-sources`, `PATCH/DELETE /admin/program-sources/{id}`
 * и `POST /admin/program-sources/{id}/check`.
 *
 * 400 значит, что ключ не соответствует формату `{org}/{code}/{year}/{form}`,
 * 409 - что такой источник уже добавлен, 403 - что раздел доступен только
 * администратору, 501 - что сервер ещё не поддерживает реестр. Технические id
 * источников на экране не показываются: только ключ, домен и данные проверки.
 *
 * Экспорт: {@link ProgramWatchTab}.
 */
import { useEffect, useState, type FormEvent } from "react"
import {
  AlertTriangleIcon,
  CheckCircle2Icon,
  ExternalLinkIcon,
  GlobeIcon,
  Loader2Icon,
  PlusIcon,
  PowerIcon,
  PowerOffIcon,
  RefreshCwIcon,
  ShieldAlertIcon,
  Trash2Icon,
} from "lucide-react"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import {
  ApiRequestError,
  checkProgramWatchSource,
  createProgramWatchSource,
  deleteProgramWatchSource,
  listProgramWatchSources,
  updateProgramWatchSource,
  type ProgramWatchCheckResult,
  type ProgramWatchSource,
  type ProgramWatchStatus,
} from "@/lib/api/client"

/** Пример строгого ключа: организация, код, год и форма обучения. */
const KEY_EXAMPLE = "mgu/01.03.02/2026/full-time"

/** Интервал проверки по умолчанию. */
const DEFAULT_INTERVAL_HOURS = 24

type ListState = "loading" | "ready" | "error" | "forbidden" | "unsupported"

const LAST_STATUS_LABELS: Record<ProgramWatchStatus, string> = {
  unchanged: "Без изменений",
  changed: "Есть изменения",
  needs_parser: "Нужен разбор",
  needs_attention: "Требует внимания",
  error: "Ошибка",
}

const LAST_STATUS_VARIANTS: Record<
  ProgramWatchStatus,
  "default" | "secondary" | "destructive" | "outline"
> = {
  unchanged: "secondary",
  changed: "default",
  needs_parser: "outline",
  needs_attention: "outline",
  error: "destructive",
}

/** Короткая дата проверки; неразобранное значение возвращается как есть. */
function formatDate(value?: string | null): string {
  if (!value) return ""
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat("ru-RU", {
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date)
}

/** Безопасная ссылка только для http и https: остальные схемы не кликабельны. */
function sourceHref(url: string): string | null {
  try {
    const parsed = new URL(url)
    if (parsed.protocol === "http:" || parsed.protocol === "https:") return parsed.toString()
    return null
  } catch {
    return null
  }
}

/** Домен для списка; некорректная ссылка показывается как есть. */
function sourceDomain(url: string): string {
  try {
    const parsed = new URL(url)
    if (parsed.protocol === "http:" || parsed.protocol === "https:") return parsed.host
  } catch {
    // Ниже вернём исходную строку: ссылка останется читаемой без перехода.
  }
  return url
}

function lastStatusLabel(status?: ProgramWatchStatus | null): string {
  if (!status) return "Не проверялся"
  return LAST_STATUS_LABELS[status] ?? status
}

function lastStatusVariant(status?: ProgramWatchStatus | null) {
  if (!status) return "outline" as const
  return LAST_STATUS_VARIANTS[status] ?? ("outline" as const)
}

/** Понятное сообщение по ошибке API; 403 и 501 переводят раздел в отдельное состояние. */
function describeError(error: unknown, fallback: string): string {
  if (error instanceof ApiRequestError) {
    if (error.status === 403) return "Раздел доступен администратору"
    if (error.status === 501) return "Сервер не поддерживает реестр"
    if (error.status === 404) return "Источник не найден. Обновите список."
    if (error.status === 0) return "Сервер недоступен: проверьте соединение."
    return error.message
  }
  return fallback
}

/** Ошибка добавления: 400 объясняет формат ключа, 409 - что источник уже есть. */
function describeCreateError(error: unknown): string {
  if (error instanceof ApiRequestError) {
    if (error.status === 400) return `Ключ должен быть вида ${KEY_EXAMPLE}`
    if (error.status === 409) return "Такой источник уже есть"
  }
  return describeError(error, "Не удалось добавить источник. Попробуйте ещё раз.")
}

/** Текст результата ручной проверки источника. */
function checkResultMessage(result: ProgramWatchCheckResult): string {
  switch (result.status) {
    case "unchanged":
      return "Изменений нет"
    case "changed":
      return "Создан черновик"
    case "needs_parser":
      return "PDF/DOCX пока не поддерживаются"
    case "error":
      return result.error?.trim() || "Не удалось проверить источник"
    default:
      return "Проверка завершена"
  }
}

export interface ProgramWatchTabProps {
  /** Открывает черновик программы, созданный проверкой источника. */
  onOpenDraft?: (draftId: string) => void
}

/**
 * Реестр источников программ: форма добавления, список источников с последней
 * проверкой и действия по строке. Рассчитан на встраивание в карточку админки.
 */
export function ProgramWatchTab({ onOpenDraft }: ProgramWatchTabProps) {
  const [sources, setSources] = useState<ProgramWatchSource[]>([])
  const [listState, setListState] = useState<ListState>("loading")
  const [listError, setListError] = useState<string | null>(null)
  const [reloadToken, setReloadToken] = useState(0)

  const [programKey, setProgramKey] = useState("")
  const [url, setUrl] = useState("")
  const [intervalHours, setIntervalHours] = useState(String(DEFAULT_INTERVAL_HOURS))
  const [docFormat, setDocFormat] = useState("")
  const [creating, setCreating] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)

  const [busyId, setBusyId] = useState<string | null>(null)
  const [deleteTargetId, setDeleteTargetId] = useState<string | null>(null)
  const [checkResult, setCheckResult] = useState<{
    source: ProgramWatchSource
    result: ProgramWatchCheckResult
  } | null>(null)

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      try {
        const items = await listProgramWatchSources()
        if (cancelled) return
        setSources(items)
        setListState("ready")
        setListError(null)
      } catch (cause) {
        if (cancelled) return
        setSources([])
        setListError(null)
        if (cause instanceof ApiRequestError && cause.status === 403) {
          setListState("forbidden")
        } else if (cause instanceof ApiRequestError && cause.status === 501) {
          setListState("unsupported")
        } else {
          setListState("error")
          setListError(describeError(cause, "Не удалось загрузить источники."))
        }
      }
    }
    // Загрузка асинхронная: состояние обновится после ответа API.
    void load()
    return () => {
      cancelled = true
    }
  }, [reloadToken])

  const reloadSources = () => {
    setListState("loading")
    setListError(null)
    setReloadToken((token) => token + 1)
  }

  /** 403 и 501 относятся ко всему разделу, а не к отдельной строке. */
  const applyAccessError = (cause: unknown): boolean => {
    if (cause instanceof ApiRequestError && cause.status === 403) {
      setListState("forbidden")
      return true
    }
    if (cause instanceof ApiRequestError && cause.status === 501) {
      setListState("unsupported")
      return true
    }
    return false
  }

  const submitSource = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (creating) return
    const key = programKey.trim()
    const sourceUrl = url.trim()
    if (!key) {
      setFormError(`Ключ должен быть вида ${KEY_EXAMPLE}`)
      return
    }
    if (!sourceUrl) {
      setFormError("Укажите ссылку на страницу программы.")
      return
    }
    const interval = Number(intervalHours)
    if (!Number.isInteger(interval) || interval < 1) {
      setFormError("Интервал должен быть целым числом часов, например 24.")
      return
    }
    const format = docFormat.trim()
    setCreating(true)
    setFormError(null)
    try {
      const created = await createProgramWatchSource({
        program_key: key,
        url: sourceUrl,
        interval_hours: interval,
        ...(format ? { doc_format: format } : {}),
      })
      setSources((previous) => [created, ...previous])
      setProgramKey("")
      setUrl("")
      setDocFormat("")
      setIntervalHours(String(DEFAULT_INTERVAL_HOURS))
      setListState("ready")
      setListError(null)
      toast.success("Источник добавлен", {
        description: "Сервер будет проверять его по расписанию.",
      })
    } catch (cause) {
      if (applyAccessError(cause)) return
      setFormError(describeCreateError(cause))
    } finally {
      setCreating(false)
    }
  }

  const runCheck = async (source: ProgramWatchSource) => {
    if (busyId !== null) return
    setBusyId(source.id)
    setCheckResult(null)
    try {
      const result = await checkProgramWatchSource(source.id)
      setCheckResult({ source, result })
      setSources((previous) =>
        previous.map((item) =>
          item.id === source.id
            ? {
                ...item,
                last_status: result.status,
                last_checked_at: new Date().toISOString(),
                last_error: result.status === "error" ? (result.error ?? null) : null,
                draft_id: result.draft_id ?? item.draft_id ?? null,
              }
            : item,
        ),
      )
    } catch (cause) {
      if (applyAccessError(cause)) return
      toast.error("Не удалось проверить источник", {
        description: describeError(cause, "Повторите попытку."),
      })
    } finally {
      setBusyId(null)
    }
  }

  const toggleSource = async (source: ProgramWatchSource) => {
    if (busyId !== null) return
    setBusyId(source.id)
    try {
      const updated = await updateProgramWatchSource(source.id, { enabled: !source.enabled })
      setSources((previous) =>
        previous.map((item) => (item.id === source.id ? { ...item, ...updated } : item)),
      )
      toast.success(updated.enabled ? "Источник включён" : "Источник выключен")
    } catch (cause) {
      if (applyAccessError(cause)) return
      toast.error("Не удалось изменить источник", {
        description: describeError(cause, "Повторите попытку."),
      })
    } finally {
      setBusyId(null)
    }
  }

  const confirmDelete = async () => {
    const target = sources.find((item) => item.id === deleteTargetId)
    if (!target || busyId !== null) return
    setBusyId(target.id)
    try {
      await deleteProgramWatchSource(target.id)
      setSources((previous) => previous.filter((item) => item.id !== target.id))
      setDeleteTargetId(null)
      if (checkResult?.source.id === target.id) setCheckResult(null)
      toast.success("Источник удалён")
    } catch (cause) {
      if (applyAccessError(cause)) return
      toast.error("Не удалось удалить источник", {
        description: describeError(cause, "Повторите попытку."),
      })
    } finally {
      setBusyId(null)
    }
  }

  if (listState === "forbidden") {
    return (
      <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed p-8 text-center">
        <div className="flex size-10 items-center justify-center rounded-full bg-muted">
          <ShieldAlertIcon className="size-5 text-muted-foreground" />
        </div>
        <div className="space-y-1">
          <p className="font-medium">Раздел доступен администратору</p>
          <p className="max-w-md text-sm text-muted-foreground">
            Реестр источников программ открыт только роли admin. Войдите с подходящей ролью,
            чтобы добавлять и проверять источники.
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={reloadSources}>
          Повторить
        </Button>
      </div>
    )
  }

  if (listState === "unsupported") {
    return (
      <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed p-8 text-center">
        <div className="flex size-10 items-center justify-center rounded-full bg-muted">
          <GlobeIcon className="size-5 text-muted-foreground" />
        </div>
        <div className="space-y-1">
          <p className="font-medium">Сервер не поддерживает реестр</p>
          <p className="max-w-md text-sm text-muted-foreground">
            Реестр источников программ на этом сервере ещё не включён. Обратитесь к
            администратору сервера.
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={reloadSources}>
          Повторить
        </Button>
      </div>
    )
  }

  const checkedDraftId =
    checkResult?.result.status === "changed" ? (checkResult.result.draft_id ?? null) : null

  return (
    <div className="space-y-5">
      <form onSubmit={(event) => void submitSource(event)} className="space-y-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="program-watch-key">Ключ программы</Label>
            <Input
              id="program-watch-key"
              value={programKey}
              onChange={(event) => setProgramKey(event.target.value)}
              placeholder={KEY_EXAMPLE}
              maxLength={200}
              required
              aria-required="true"
              disabled={creating}
            />
            <p className="text-xs text-muted-foreground">
              Формат: организация/код/год/форма обучения.
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="program-watch-url">Ссылка на программу</Label>
            <Input
              id="program-watch-url"
              type="url"
              value={url}
              onChange={(event) => setUrl(event.target.value)}
              placeholder="https://example.ru/programs/01.03.02"
              maxLength={2000}
              required
              aria-required="true"
              disabled={creating}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="program-watch-interval">Интервал проверки, часы</Label>
            <Input
              id="program-watch-interval"
              type="number"
              min={1}
              step={1}
              value={intervalHours}
              onChange={(event) => setIntervalHours(event.target.value)}
              disabled={creating}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="program-watch-format">Формат документа (необязательно)</Label>
            <Input
              id="program-watch-format"
              value={docFormat}
              onChange={(event) => setDocFormat(event.target.value)}
              placeholder="Например, html"
              maxLength={40}
              disabled={creating}
            />
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button type="submit" disabled={!programKey.trim() || !url.trim() || creating}>
            {creating ? (
              <Loader2Icon className="size-4 animate-spin" />
            ) : (
              <PlusIcon className="size-4" />
            )}
            {creating ? "Добавление..." : "Добавить источник"}
          </Button>
          <p className="text-xs text-muted-foreground">
            Сервер проверяет источник по расписанию, запустить проверку можно вручную.
          </p>
        </div>
        {formError && (
          <p role="alert" className="text-sm text-destructive">
            {formError}
          </p>
        )}
      </form>

      {checkResult && (
        <div role="status" className="space-y-2 rounded-lg border bg-muted/20 p-3">
          <p className="text-xs text-muted-foreground">
            Проверка источника {checkResult.source.program_key}
          </p>
          <p className="flex items-center gap-2 text-sm font-medium">
            {checkResult.result.status === "error" ? (
              <AlertTriangleIcon className="size-4 shrink-0 text-destructive" />
            ) : (
              <CheckCircle2Icon className="size-4 shrink-0 text-success" />
            )}
            {checkResultMessage(checkResult.result)}
          </p>
          {checkedDraftId && onOpenDraft && (
            <Button variant="outline" size="sm" onClick={() => onOpenDraft(checkedDraftId)}>
              <ExternalLinkIcon className="size-3.5" />
              Открыть
            </Button>
          )}
        </div>
      )}

      <section className="space-y-2">
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-sm font-medium">Источники</h3>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label="Перезагрузить список источников"
            disabled={listState === "loading"}
            onClick={reloadSources}
          >
            <RefreshCwIcon className={`size-4 ${listState === "loading" ? "animate-spin" : ""}`} />
          </Button>
        </div>

        {listState === "loading" ? (
          <div className="space-y-2">
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
          </div>
        ) : listState === "error" ? (
          <div className="space-y-2 rounded-lg border border-destructive/50 p-3 text-sm">
            <p className="text-destructive" role="alert">
              {listError ?? "Не удалось загрузить источники."}
            </p>
            <Button variant="outline" size="sm" onClick={reloadSources}>
              Повторить загрузку
            </Button>
          </div>
        ) : sources.length === 0 ? (
          <div className="space-y-1 rounded-lg border border-dashed p-6 text-center">
            <p className="font-medium">Источников пока нет.</p>
            <p className="text-sm text-muted-foreground">
              Добавьте ссылку на страницу программы, чтобы сервер проверял её по расписанию и
              создавал черновики при изменениях.
            </p>
          </div>
        ) : (
          <ul className="divide-y overflow-hidden rounded-lg border">
            {sources.map((source) => {
              const href = sourceHref(source.url)
              const draftId = source.draft_id ?? null
              return (
                <li key={source.id} className="space-y-2 px-3 py-3">
                  <div className="flex flex-wrap items-start justify-between gap-2">
                    <div className="min-w-0 space-y-0.5">
                      <p className="break-words text-sm font-medium">{source.program_key}</p>
                      <p className="truncate text-xs text-muted-foreground">
                        {href ? (
                          <a
                            href={href}
                            target="_blank"
                            rel="noreferrer noopener"
                            className="hover:underline"
                            title={source.url}
                          >
                            {sourceDomain(source.url)}
                          </a>
                        ) : (
                          sourceDomain(source.url)
                        )}
                        {" · "}
                        каждые {source.interval_hours} ч
                        {source.doc_format ? ` · ${source.doc_format}` : ""}
                      </p>
                    </div>
                    <div className="flex flex-wrap items-center gap-1.5">
                      {!source.enabled && <Badge variant="outline">Выключен</Badge>}
                      <Badge variant={lastStatusVariant(source.last_status)}>
                        {lastStatusLabel(source.last_status)}
                      </Badge>
                    </div>
                  </div>

                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <p className="text-xs text-muted-foreground">
                      {source.last_checked_at ? (
                        <>
                          Проверен:{" "}
                          <time dateTime={source.last_checked_at}>
                            {formatDate(source.last_checked_at)}
                          </time>
                        </>
                      ) : (
                        "Ещё не проверялся"
                      )}
                    </p>
                    {deleteTargetId !== source.id && (
                      <div className="flex flex-wrap items-center gap-1.5">
                        {draftId && onOpenDraft && (
                          <Button
                            type="button"
                            variant="link"
                            size="xs"
                            onClick={() => onOpenDraft(draftId)}
                          >
                            Черновик
                          </Button>
                        )}
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          disabled={busyId !== null}
                          onClick={() => void runCheck(source)}
                        >
                          {busyId === source.id ? (
                            <Loader2Icon className="size-3.5 animate-spin" />
                          ) : (
                            <RefreshCwIcon className="size-3.5" />
                          )}
                          Проверить сейчас
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          disabled={busyId !== null}
                          onClick={() => void toggleSource(source)}
                        >
                          {source.enabled ? (
                            <PowerOffIcon className="size-3.5" />
                          ) : (
                            <PowerIcon className="size-3.5" />
                          )}
                          {source.enabled ? "Выключить" : "Включить"}
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          className="text-muted-foreground hover:text-destructive"
                          disabled={busyId !== null}
                          onClick={() => setDeleteTargetId(source.id)}
                        >
                          <Trash2Icon className="size-3.5" />
                          Удалить
                        </Button>
                      </div>
                    )}
                  </div>

                  {deleteTargetId === source.id && (
                    <div className="flex flex-wrap items-center gap-2 rounded-lg border border-destructive/40 bg-destructive/5 p-2">
                      <p className="text-sm">Удалить источник?</p>
                      <Button
                        type="button"
                        variant="destructive"
                        size="xs"
                        disabled={busyId !== null}
                        onClick={() => void confirmDelete()}
                      >
                        {busyId === source.id ? (
                          <Loader2Icon className="size-3 animate-spin" />
                        ) : null}
                        Удалить
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="xs"
                        disabled={busyId !== null}
                        onClick={() => setDeleteTargetId(null)}
                      >
                        Отмена
                      </Button>
                    </div>
                  )}
                </li>
              )
            })}
          </ul>
        )}
      </section>
    </div>
  )
}
