/**
 * @file program-import-tab.tsx
 * @description Импорт образовательной программы: загрузка файла с кодом программы,
 * проверка черновика, публикация с отчётом о несопоставленных элементах и отклонение.
 *
 * Данные: `POST /admin/programs/import` (multipart: `file` и `key`),
 * `GET /admin/programs/drafts`, `GET /admin/programs/drafts/{id}`,
 * `POST /admin/programs/drafts/{id}/publish` и
 * `POST /admin/programs/drafts/{id}/reject`.
 *
 * Ошибка разбора приходит со статусом 422: показываем сообщение и предупреждения
 * из тела ответа. 403 означает, что раздел доступен только администратору, 501 -
 * что сервер ещё не поддерживает импорт. Технические id черновика и программы
 * в интерфейсе не показываются: только код программы, название и организация.
 *
 * Экспорт: {@link ProgramImportTab}.
 */
import { Fragment, useEffect, useState, type FormEvent } from "react"
import {
  AlertTriangleIcon,
  CheckCircle2Icon,
  ChevronDownIcon,
  FileUpIcon,
  GraduationCapIcon,
  Loader2Icon,
  RefreshCwIcon,
  ShieldAlertIcon,
  XIcon,
} from "lucide-react"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import {
  ApiRequestError,
  getProgramDraft,
  importProgram,
  listProgramDrafts,
  publishProgramDraft,
  rejectProgramDraft,
  type ProgramDraftDetail,
  type ProgramDraftSummary,
  type ProgramImportResponse,
  type ProgramPublishResult,
} from "@/lib/api/client"

/** Форматы файлов программы, которые принимает импорт. */
const IMPORT_ACCEPT = ".csv,.xlsx,.json,.txt,.pdf,.docx"

const DRAFT_STATUS_LABELS: Record<string, string> = {
  draft: "Черновик",
  published: "Опубликован",
  rejected: "Отклонён",
}

const DRAFT_STATUS_VARIANTS: Record<string, "default" | "secondary" | "destructive" | "outline"> = {
  draft: "secondary",
  published: "default",
  rejected: "destructive",
}

const KSA_KIND_LABELS: Record<string, string> = {
  knowledge: "Знание",
  skill: "Умение",
  ability: "Навык",
  знание: "Знание",
  умение: "Умение",
  навык: "Навык",
}

/** Тип компетенции приходит отдельным полем; из кода он не выводится. */
const COMPETENCY_TYPE_LABELS: Record<string, string> = {
  universal: "УК",
  general_professional: "ОПК",
  professional: "ПК",
  other: "иное",
}

/** Ошибка загрузки: сообщение сервера и предупреждения разбора (для 422). */
interface ImportFormError {
  message: string
  warnings: string[]
}

type ListState = "loading" | "ready" | "error" | "forbidden" | "unsupported"

/** Короткая дата создания черновика; неразобранное значение возвращается как есть. */
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

function draftStatusLabel(status?: string | null): string {
  if (!status) return "Статус неизвестен"
  return DRAFT_STATUS_LABELS[status] ?? status
}

function draftStatusVariant(status?: string | null) {
  if (!status) return "outline" as const
  return DRAFT_STATUS_VARIANTS[status] ?? ("outline" as const)
}

function ksaKindLabel(kind?: string | null): string {
  if (!kind) return "KSA"
  return KSA_KIND_LABELS[kind.toLowerCase()] ?? kind
}

/** Русская подпись типа компетенции; пустое значение показывается дефисом. */
function competencyTypeLabel(type?: string | null): string {
  const value = type?.trim()
  if (!value) return "-"
  return COMPETENCY_TYPE_LABELS[value] ?? value
}

/** Понятное сообщение по ошибке API; 403 и 501 обрабатываются отдельными состояниями. */
function describeError(error: unknown, fallback: string): string {
  if (error instanceof ApiRequestError) {
    if (error.status === 403) return "Раздел доступен администратору."
    if (error.status === 501) return "Сервер не поддерживает импорт."
    if (error.status === 0) return "Сервер недоступен: проверьте соединение."
    return error.message
  }
  return fallback
}

/** Сообщение для неудачной загрузки файла: 422 сохраняет текст разбора и предупреждения. */
function describeImportFailure(error: ApiRequestError): string {
  if (error.status === 422) {
    return error.message.startsWith("Запрос завершился")
      ? "Не удалось разобрать файл. Проверьте формат и содержимое."
      : error.message
  }
  if (error.status === 409) return "Черновик уже опубликован или отклонён. Обновите список."
  return describeError(error, "Не удалось загрузить программу. Попробуйте ещё раз.")
}

/** Сообщение для публикации и отклонения: 409 объясняет, что решение уже принято. */
function describeActionFailure(error: ApiRequestError, fallback: string): string {
  if (error.status === 409) {
    return error.message.startsWith("Запрос завершился") || !error.message.trim()
      ? "Черновик уже опубликован или отклонён. Обновите список."
      : error.message
  }
  return describeError(error, fallback)
}

/** Черновик, который нужно открыть по запросу снаружи; `seq` различает повторные клики. */
export interface ProgramImportDraftRequest {
  id: string
  seq: number
}

export interface ProgramImportTabProps {
  /**
   * Черновик, который нужно открыть сразу: например, после проверки источника
   * программы в соседней карточке реестра.
   */
  openDraftRequest?: ProgramImportDraftRequest | null
}

/**
 * Импорт образовательной программы: загрузка, карточка черновика и список
 * последних черновиков. Компонент рассчитан на встраивание в карточку админки.
 */
export function ProgramImportTab({ openDraftRequest }: ProgramImportTabProps) {
  const [drafts, setDrafts] = useState<ProgramDraftSummary[]>([])
  const [listState, setListState] = useState<ListState>("loading")
  const [listError, setListError] = useState<string | null>(null)
  const [reloadToken, setReloadToken] = useState(0)

  const [file, setFile] = useState<File | null>(null)
  const [fileInputKey, setFileInputKey] = useState(0)
  const [programKey, setProgramKey] = useState("")
  const [uploading, setUploading] = useState(false)
  const [uploadError, setUploadError] = useState<ImportFormError | null>(null)

  const [draft, setDraft] = useState<ProgramDraftDetail | null>(null)
  const [openingId, setOpeningId] = useState<string | null>(null)
  const [expandedKsa, setExpandedKsa] = useState<Record<string, boolean>>({})
  const [taxonomy, setTaxonomy] = useState("")
  const [publishing, setPublishing] = useState(false)
  const [rejecting, setRejecting] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const [publishResult, setPublishResult] = useState<ProgramPublishResult | null>(null)

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      try {
        const items = await listProgramDrafts()
        if (cancelled) return
        setDrafts(items)
        setListState("ready")
        setListError(null)
      } catch (cause) {
        if (cancelled) return
        setDrafts([])
        setListError(null)
        if (cause instanceof ApiRequestError && cause.status === 403) {
          setListState("forbidden")
        } else if (cause instanceof ApiRequestError && cause.status === 501) {
          setListState("unsupported")
        } else {
          setListState("error")
          setListError(describeError(cause, "Не удалось загрузить черновики."))
        }
      }
    }
    // Загрузка асинхронная: состояние обновится после ответа API.
    void load()
    return () => {
      cancelled = true
    }
  }, [reloadToken])

  useEffect(() => {
    const request = openDraftRequest
    if (!request?.id) return
    let cancelled = false
    const load = async () => {
      try {
        const detail = await getProgramDraft(request.id)
        if (cancelled) return
        setDraft({
          ...detail,
          id: request.id,
          program_key: detail.program_key,
          status: detail.status,
        })
        setExpandedKsa({})
        setTaxonomy("")
        setPublishResult(null)
        setActionError(null)
        setUploadError(null)
      } catch (cause) {
        if (cancelled) return
        if (cause instanceof ApiRequestError && cause.status === 403) {
          setListState("forbidden")
          return
        }
        if (cause instanceof ApiRequestError && cause.status === 501) {
          setListState("unsupported")
          return
        }
        toast.error("Не удалось открыть черновик", {
          description: describeError(cause, "Повторите попытку."),
        })
      }
    }
    // Запрос приходит из соседней карточки: карточка черновика обновится после ответа API.
    void load()
    return () => {
      cancelled = true
    }
  }, [openDraftRequest])

  const reloadDrafts = () => {
    setListState("loading")
    setListError(null)
    setReloadToken((token) => token + 1)
  }

  /** Тихо обновляет список после решения по черновику, не скрывая карточку. */
  const refreshDrafts = async () => {
    try {
      const items = await listProgramDrafts()
      setDrafts(items)
      setListState("ready")
      setListError(null)
    } catch {
      // Список обновится при следующем открытии раздела; карточка черновика уже показана.
    }
  }

  const takeDraft = (response: ProgramImportResponse, key: string) => {
    setDraft({
      id: response.draft_id,
      program_key: key,
      status: response.status,
      program: response.program,
      report: response.report,
    })
    setExpandedKsa({})
    setTaxonomy("")
    setPublishResult(null)
    setActionError(null)
  }

  const submitImport = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (uploading) return
    const key = programKey.trim()
    if (!file) {
      setUploadError({ message: "Выберите файл программы.", warnings: [] })
      return
    }
    if (!key) {
      setUploadError({ message: "Укажите код программы.", warnings: [] })
      return
    }
    setUploading(true)
    setUploadError(null)
    try {
      const response = await importProgram(file, key)
      takeDraft(response, key)
      setFile(null)
      setProgramKey("")
      setFileInputKey((value) => value + 1)
      toast.success("Черновик создан", {
        description: "Проверьте компетенции и предупреждения, затем опубликуйте программу.",
      })
      void refreshDrafts()
    } catch (cause) {
      if (cause instanceof ApiRequestError && cause.status === 403) {
        setListState("forbidden")
        return
      }
      if (cause instanceof ApiRequestError && cause.status === 501) {
        setListState("unsupported")
        return
      }
      setUploadError({
        message:
          cause instanceof ApiRequestError
            ? describeImportFailure(cause)
            : "Не удалось загрузить программу. Попробуйте ещё раз.",
        warnings: cause instanceof ApiRequestError ? cause.warnings : [],
      })
    } finally {
      setUploading(false)
    }
  }

  const openDraft = async (summary: ProgramDraftSummary) => {
    if (openingId) return
    setOpeningId(summary.id)
    setActionError(null)
    setPublishResult(null)
    setUploadError(null)
    try {
      const detail = await getProgramDraft(summary.id)
      setDraft({
        ...detail,
        id: summary.id,
        program_key: detail.program_key ?? summary.program_key,
        source_name: detail.source_name ?? summary.source_name,
        status: detail.status ?? summary.status,
      })
      setExpandedKsa({})
      setTaxonomy("")
    } catch (cause) {
      if (cause instanceof ApiRequestError && cause.status === 403) {
        setListState("forbidden")
        return
      }
      if (cause instanceof ApiRequestError && cause.status === 501) {
        setListState("unsupported")
        return
      }
      toast.error("Не удалось открыть черновик", {
        description: describeError(cause, "Повторите попытку."),
      })
    } finally {
      setOpeningId(null)
    }
  }

  const submitPublish = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!draft?.id || publishing || rejecting) return
    setPublishing(true)
    setActionError(null)
    try {
      const result = await publishProgramDraft(draft.id, taxonomy.trim() || undefined)
      setPublishResult(result)
      setDraft((previous) => (previous ? { ...previous, status: "published" } : previous))
      toast.success("Программа опубликована")
      void refreshDrafts()
    } catch (cause) {
      if (cause instanceof ApiRequestError && cause.status === 403) {
        setListState("forbidden")
        return
      }
      if (cause instanceof ApiRequestError && cause.status === 501) {
        setListState("unsupported")
        return
      }
      setActionError(
        cause instanceof ApiRequestError
          ? describeActionFailure(cause, "Не удалось опубликовать программу. Повторите попытку.")
          : "Не удалось опубликовать программу. Повторите попытку.",
      )
    } finally {
      setPublishing(false)
    }
  }

  const rejectDraft = async () => {
    if (!draft?.id || publishing || rejecting) return
    setRejecting(true)
    setActionError(null)
    try {
      await rejectProgramDraft(draft.id)
      setDraft((previous) => (previous ? { ...previous, status: "rejected" } : previous))
      setPublishResult(null)
      toast.success("Черновик отклонён")
      void refreshDrafts()
    } catch (cause) {
      if (cause instanceof ApiRequestError && cause.status === 403) {
        setListState("forbidden")
        return
      }
      if (cause instanceof ApiRequestError && cause.status === 501) {
        setListState("unsupported")
        return
      }
      setActionError(
        cause instanceof ApiRequestError
          ? describeActionFailure(cause, "Не удалось отклонить черновик. Повторите попытку.")
          : "Не удалось отклонить черновик. Повторите попытку.",
      )
    } finally {
      setRejecting(false)
    }
  }

  const toggleKsa = (key: string) => {
    setExpandedKsa((previous) => ({ ...previous, [key]: !previous[key] }))
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
            Импорт образовательных программ открыт только роли admin. Войдите с подходящей
            ролью, чтобы загружать файлы и публиковать черновики.
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={reloadDrafts}>
          Повторить
        </Button>
      </div>
    )
  }

  if (listState === "unsupported") {
    return (
      <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed p-8 text-center">
        <div className="flex size-10 items-center justify-center rounded-full bg-muted">
          <FileUpIcon className="size-5 text-muted-foreground" />
        </div>
        <div className="space-y-1">
          <p className="font-medium">Сервер не поддерживает импорт</p>
          <p className="max-w-md text-sm text-muted-foreground">
            Импорт образовательных программ на этом сервере ещё не включён. Обратитесь к
            администратору сервера.
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={reloadDrafts}>
          Повторить
        </Button>
      </div>
    )
  }

  const program = draft?.program
  const competencies = Array.isArray(program?.competencies) ? program.competencies : []
  const computedKsa = competencies.reduce(
    (total, competency) =>
      total + (Array.isArray(competency.ksa) ? competency.ksa.length : 0),
    0,
  )
  const competencyTotal =
    typeof draft?.report?.competencies === "number" ? draft.report.competencies : competencies.length
  const ksaTotal = typeof draft?.report?.ksa === "number" ? draft.report.ksa : computedKsa
  const warnings = Array.isArray(draft?.report?.warnings) ? draft.report.warnings : []
  const draftStatus = draft?.status ?? "draft"
  const canDecide = draftStatus === "draft"
  const draftBusy = publishing || rejecting
  const unknownTaxonomyCodes = Array.isArray(publishResult?.unknown_taxonomy_codes)
    ? publishResult.unknown_taxonomy_codes
    : []

  return (
    <div className="space-y-5">
      <form onSubmit={(event) => void submitImport(event)} className="space-y-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="program-import-key">Код программы</Label>
            <Input
              id="program-import-key"
              value={programKey}
              onChange={(event) => setProgramKey(event.target.value)}
              placeholder="Например, 01.03.02"
              maxLength={120}
              required
              aria-required="true"
              disabled={uploading}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="program-import-file">Файл программы</Label>
            <Input
              key={fileInputKey}
              id="program-import-file"
              type="file"
              accept={IMPORT_ACCEPT}
              onChange={(event) => setFile(event.target.files?.[0] ?? null)}
              disabled={uploading}
            />
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button type="submit" disabled={!file || !programKey.trim() || uploading}>
            {uploading ? (
              <Loader2Icon className="size-4 animate-spin" />
            ) : (
              <FileUpIcon className="size-4" />
            )}
            {uploading ? "Загрузка..." : "Загрузить"}
          </Button>
          <p className="text-xs text-muted-foreground">
            CSV, XLSX, JSON, TXT, PDF или DOCX. PDF и DOCX разбирает LLM. Код программы обязателен.
          </p>
        </div>

        {uploadError && (
          <div
            role="alert"
            className="space-y-2 rounded-lg border border-destructive/50 bg-destructive/5 p-3 text-sm"
          >
            <p className="font-medium text-destructive">{uploadError.message}</p>
            {uploadError.warnings.length > 0 && (
              <div className="space-y-1">
                <p className="text-xs font-medium text-muted-foreground">
                  Предупреждения разбора ({uploadError.warnings.length})
                </p>
                <ul className="list-disc space-y-1 pl-5 text-sm text-muted-foreground">
                  {uploadError.warnings.map((warning, index) => (
                    <li key={`${index}-${warning}`}>{warning}</li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        )}
      </form>

      {draft && (
        <section
          aria-label="Черновик программы"
          className="space-y-3 rounded-lg border bg-muted/10 p-4"
        >
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div className="min-w-0 space-y-0.5">
              <h3 className="flex items-center gap-2 break-words text-base font-medium">
                <GraduationCapIcon className="size-4 shrink-0" />
                {program?.title?.trim() || "Без названия"}
              </h3>
              {draft.program_key && (
                <p className="text-xs text-muted-foreground">Код программы: {draft.program_key}</p>
              )}
            </div>
            <Badge variant={draftStatusVariant(draftStatus)}>{draftStatusLabel(draftStatus)}</Badge>
          </div>

          <div className="grid gap-2 sm:grid-cols-2">
            <div className="space-y-0.5">
              <p className="text-xs text-muted-foreground">Код</p>
              <p className="text-sm font-medium">{program?.code?.trim() || "Не указан"}</p>
            </div>
            <div className="space-y-0.5">
              <p className="text-xs text-muted-foreground">Организация</p>
              <p className="text-sm font-medium">{program?.organization?.trim() || "Не указана"}</p>
            </div>
          </div>

          <p className="text-xs text-muted-foreground">
            Компетенций: {competencyTotal} · KSA: {ksaTotal}
          </p>

          {publishResult && (
            <div
              role="status"
              className="space-y-1 rounded-lg border border-success/40 bg-success/10 p-3"
            >
              <p className="flex items-center gap-2 text-sm font-medium text-success">
                <CheckCircle2Icon className="size-4" />
                Программа опубликована
              </p>
              <p className="text-sm">
                Не сопоставлено компетенций: {publishResult.unmatched_competencies ?? 0}
              </p>
              <p className="text-sm">Не сопоставлено KSA: {publishResult.unmatched_ksa ?? 0}</p>
              <p className="text-sm">
                Неизвестные коды таксономии:{" "}
                {unknownTaxonomyCodes.length > 0 ? unknownTaxonomyCodes.join(", ") : "нет"}
              </p>
            </div>
          )}

          {warnings.length > 0 && (
            <div className="space-y-1 rounded-lg border border-warning/40 bg-warning/10 p-3">
              <p className="flex items-center gap-2 text-sm font-medium text-warning">
                <AlertTriangleIcon className="size-4" />
                Предупреждения ({warnings.length})
              </p>
              <ul className="list-disc space-y-1 pl-5 text-sm text-muted-foreground">
                {warnings.map((warning, index) => (
                  <li key={`${index}-${warning}`}>{warning}</li>
                ))}
              </ul>
            </div>
          )}

          <div className="overflow-x-auto rounded-lg border bg-background">
            <table className="w-full text-left text-sm">
              <thead className="bg-muted/30 text-xs text-muted-foreground">
                <tr className="border-b">
                  <th className="px-3 py-2 font-medium">Код</th>
                  <th className="px-3 py-2 font-medium">Тип</th>
                  <th className="px-3 py-2 font-medium">Формулировка</th>
                  <th className="px-3 py-2 font-medium">KSA</th>
                </tr>
              </thead>
              <tbody>
                {competencies.length === 0 ? (
                  <tr>
                    <td colSpan={4} className="px-3 py-3 text-muted-foreground">
                      В программе нет компетенций.
                    </td>
                  </tr>
                ) : (
                  competencies.map((competency, index) => {
                    const rowKey = `${competency.code ?? "competency"}-${index}`
                    const ksa = Array.isArray(competency.ksa) ? competency.ksa : []
                    const expanded = Boolean(expandedKsa[rowKey])
                    return (
                      <Fragment key={rowKey}>
                        <tr className="border-b align-top last:border-b-0">
                          <td className="px-3 py-2 font-mono text-xs">
                            {competency.code?.trim() || "-"}
                          </td>
                          <td className="px-3 py-2">{competencyTypeLabel(competency.type)}</td>
                          <td className="px-3 py-2">
                            <p className="break-words">{competency.title?.trim() || "-"}</p>
                            {Array.isArray(competency.taxonomy_codes) &&
                              competency.taxonomy_codes.length > 0 && (
                                <p className="mt-0.5 text-xs text-muted-foreground">
                                  Коды таксономии: {competency.taxonomy_codes.join(", ")}
                                </p>
                              )}
                          </td>
                          <td className="px-3 py-2">
                            {ksa.length > 0 ? (
                              <Button
                                type="button"
                                variant="ghost"
                                size="xs"
                                aria-expanded={expanded}
                                aria-label={`${expanded ? "Скрыть" : "Показать"} KSA: ${
                                  competency.title?.trim() || "компетенция"
                                }`}
                                onClick={() => toggleKsa(rowKey)}
                              >
                                {ksa.length}
                                <ChevronDownIcon
                                  className={`size-3.5 transition-transform ${
                                    expanded ? "rotate-180" : ""
                                  }`}
                                />
                              </Button>
                            ) : (
                              <span className="text-muted-foreground">0</span>
                            )}
                          </td>
                        </tr>
                        {expanded && ksa.length > 0 && (
                          <tr className="border-b bg-muted/20 last:border-b-0">
                            <td colSpan={4} className="px-3 py-2">
                              <ul className="space-y-1.5">
                                {ksa.map((item, ksaIndex) => (
                                  <li
                                    key={`${rowKey}-ksa-${ksaIndex}`}
                                    className="rounded-md border bg-background px-2.5 py-2"
                                  >
                                    <div className="flex flex-wrap items-center gap-2">
                                      <Badge variant="outline">{ksaKindLabel(item.kind)}</Badge>
                                      <span className="min-w-0 flex-1 break-words">
                                        {item.title?.trim() || "-"}
                                      </span>
                                    </div>
                                    {Array.isArray(item.taxonomy_codes) &&
                                      item.taxonomy_codes.length > 0 && (
                                        <p className="mt-1 text-xs text-muted-foreground">
                                          Коды таксономии: {item.taxonomy_codes.join(", ")}
                                        </p>
                                      )}
                                  </li>
                                ))}
                              </ul>
                            </td>
                          </tr>
                        )}
                      </Fragment>
                    )
                  })
                )}
              </tbody>
            </table>
          </div>

          {actionError && (
            <p role="alert" className="text-sm text-destructive">
              {actionError}
            </p>
          )}

          {canDecide ? (
            <form
              onSubmit={(event) => void submitPublish(event)}
              className="space-y-2 rounded-lg border bg-background p-3"
            >
              <div className="max-w-sm space-y-1.5">
                <Label htmlFor="program-draft-taxonomy">Таксономия</Label>
                <Input
                  id="program-draft-taxonomy"
                  value={taxonomy}
                  onChange={(event) => setTaxonomy(event.target.value)}
                  placeholder="Необязательно: код или название таксономии"
                  disabled={draftBusy}
                />
              </div>
              <div className="flex flex-wrap gap-2">
                <Button type="submit" disabled={draftBusy}>
                  {publishing ? (
                    <Loader2Icon className="size-4 animate-spin" />
                  ) : (
                    <CheckCircle2Icon className="size-4" />
                  )}
                  {publishing ? "Публикация..." : "Опубликовать"}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  disabled={draftBusy}
                  onClick={() => void rejectDraft()}
                >
                  {rejecting ? <Loader2Icon className="size-4 animate-spin" /> : <XIcon className="size-4" />}
                  {rejecting ? "Отклонение..." : "Отклонить"}
                </Button>
              </div>
            </form>
          ) : (
            <p className="rounded-lg border bg-background p-3 text-sm text-muted-foreground">
              {draftStatus === "published"
                ? "Черновик уже опубликован."
                : draftStatus === "rejected"
                  ? "Черновик отклонён."
                  : "Решение по черновику уже принято."}
            </p>
          )}
        </section>
      )}

      <section className="space-y-2">
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-sm font-medium">Последние черновики</h3>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label="Перезагрузить список черновиков"
            disabled={listState === "loading"}
            onClick={reloadDrafts}
          >
            <RefreshCwIcon className={`size-4 ${listState === "loading" ? "animate-spin" : ""}`} />
          </Button>
        </div>

        {listState === "loading" ? (
          <div className="space-y-2">
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
          </div>
        ) : listState === "error" ? (
          <div className="space-y-2 rounded-lg border border-destructive/50 p-3 text-sm">
            <p className="text-destructive" role="alert">
              {listError ?? "Не удалось загрузить черновики."}
            </p>
            <Button variant="outline" size="sm" onClick={reloadDrafts}>
              Повторить загрузку черновиков
            </Button>
          </div>
        ) : drafts.length === 0 ? (
          <div className="space-y-1 rounded-lg border border-dashed p-6 text-center">
            <p className="font-medium">Черновиков пока нет.</p>
            <p className="text-sm text-muted-foreground">
              Загрузите файл программы, чтобы создать первый черновик и проверить компетенции.
            </p>
          </div>
        ) : (
          <ul className="divide-y overflow-hidden rounded-lg border">
            {drafts.map((item) => (
              <li key={item.id} className="flex flex-wrap items-center gap-2 px-3 py-2.5">
                <div className="min-w-0 flex-1 space-y-0.5">
                  <p className="truncate text-sm font-medium">{item.program_key?.trim() || "Без кода"}</p>
                  <p className="truncate text-xs text-muted-foreground">
                    {[
                      item.source_name,
                      formatDate(item.created_at),
                      `Компетенций: ${item.competencies ?? 0}`,
                      `KSA: ${item.ksa ?? 0}`,
                    ]
                      .filter((value) => value.trim() !== "")
                      .join(" · ")}
                  </p>
                </div>
                <Badge variant={draftStatusVariant(item.status)}>
                  {draftStatusLabel(item.status)}
                </Badge>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={openingId !== null}
                  onClick={() => void openDraft(item)}
                >
                  {openingId === item.id ? (
                    <Loader2Icon className="size-3.5 animate-spin" />
                  ) : null}
                  Открыть
                </Button>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}
