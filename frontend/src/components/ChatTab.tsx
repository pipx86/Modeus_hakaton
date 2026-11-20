/**
 * @file ChatTab.tsx
 * @description Вкладка чата: история интервью, отправка ответов и просмотр после завершения.
 */

import { useEffect, useRef, useState, type KeyboardEvent } from "react"
import { Link } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import { type Run, type RunStatus } from "@/lib/api/client"
import { getInterviewProgress, type InterviewProgress, attachInterviewEvidence, attachInterviewLink } from "@/lib/api/client"
import { downloadText } from "@/lib/export-tree"
import { chatTranscriptToMarkdown } from "@/lib/export-chat"
import { useChatStream } from "@/hooks/use-chat-stream"
import { toast } from "sonner"
import {
  DownloadIcon,
  InfoIcon,
  Loader2Icon,
  PaperclipIcon,
  LinkIcon,
  RefreshCwIcon,
  SendIcon,
  WifiOffIcon,
} from "lucide-react"

const URL_PATTERN = /https?:\/\/[^\s<>"']+/g

/** Превращает URL в кликабельные ссылки, не затрагивая остальной текст. */
function linkify(text: string) {
  const parts: Array<string | { url: string }> = []
  let lastIndex = 0
  for (const match of text.matchAll(URL_PATTERN)) {
    const index = match.index ?? 0
    if (index > lastIndex) parts.push(text.slice(lastIndex, index))
    let url = match[0]
    const trailing = url.match(/[.,;:!?)]+$/)
    if (trailing) url = url.slice(0, -trailing[0].length)
    parts.push({ url })
    lastIndex = index + url.length
  }
  if (lastIndex < text.length) parts.push(text.slice(lastIndex))
  return parts.map((part, index) =>
    typeof part === "string" ? (
      <span key={index}>{part}</span>
    ) : (
      <a
        key={index}
        href={part.url}
        target="_blank"
        rel="noopener noreferrer"
        className="text-primary underline underline-offset-2 hover:opacity-80"
      >
        {part.url}
      </a>
    ),
  )
}

interface ChatTabProps {
  runId: string
  active: boolean
  runStatus?: RunStatus
  stage?: Run["stage"]
  /** Цитата, к которой нужно прокрутить историю (переход из диалога обоснования). */
  focus?: { refId?: string; quote: string } | null
  /** Только просмотр: композер и отправка скрыты. */
  readOnly?: boolean
}

export function ChatTab({ runId, active, runStatus, stage, focus, readOnly = false }: ChatTabProps) {
  const {
    messages,
    loading,
    loadError,
    draft,
    setDraft,
    sending,
    thinking,
    streamState,
    chatClosed,
    interviewFinished,
    send,
    retry,
  } = useChatStream({ runId, active, runStatus, stage })
  const bottomRef = useRef<HTMLDivElement>(null)
  const [highlightedId, setHighlightedId] = useState<string | null>(null)
  const [progress, setProgress] = useState<InterviewProgress | null>(null)
  const [attaching, setAttaching] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)
  const appliedFocus = useRef<string | null>(null)

  useEffect(() => {
    if (!active) return
    let cancelled = false
    void getInterviewProgress(runId)
      .then((value) => {
        if (!cancelled) setProgress(value)
      })
      .catch(() => {
        if (!cancelled) setProgress(null)
      })
    return () => {
      cancelled = true
    }
  }, [active, runId, messages.length])

  const renderContent = (message: (typeof messages)[number]) => {
    if (highlightedId !== message.id || !focus?.quote) return linkify(message.content)
    const index = message.content.toLowerCase().indexOf(focus.quote.toLowerCase())
    if (index < 0) return linkify(message.content)
    return (
      <>
        {linkify(message.content.slice(0, index))}
        <mark className="rounded bg-warning/30 px-0.5 text-inherit">
          {message.content.slice(index, index + focus.quote.length)}
        </mark>
        {linkify(message.content.slice(index + focus.quote.length))}
      </>
    )
  }

  useEffect(() => {
    bottomRef.current?.scrollIntoView?.({ behavior: "smooth", block: "end" })
  }, [messages, thinking])

  useEffect(() => {
    if (!focus) return
    const key = focus.refId || focus.quote
    if (appliedFocus.current === key) return
    const target =
      messages.find((message) => focus.refId && message.id === focus.refId) ??
      messages.find((message) => message.content.includes(focus.quote))
    if (!target) return
    appliedFocus.current = key
    // Даём DOM дорисовать сообщение, затем центрируем его и подсвечиваем.
    const frame = window.requestAnimationFrame(() => {
      document
        .getElementById(`chat-message-${target.id}`)
        ?.scrollIntoView?.({ behavior: "smooth", block: "center" })
      setHighlightedId(target.id)
    })
    const timer = window.setTimeout(() => setHighlightedId(null), 4000)
    return () => {
      window.cancelAnimationFrame(frame)
      window.clearTimeout(timer)
    }
  }, [focus, messages])

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault()
      void send()
    }
  }

  const attachFile = async (file: File | undefined) => {
    if (!file) return
    setAttaching(true)
    try {
      await attachInterviewEvidence(runId, file)
      await retry()
      toast.success("Доказательство добавлено в интервью")
    } catch (cause) {
      toast.error("Не удалось прикрепить файл", {
        description: cause instanceof Error ? cause.message : "Поддерживаются PDF, DOCX, TXT и MD.",
      })
    } finally {
      setAttaching(false)
      if (fileInput.current) fileInput.current.value = ""
    }
  }

  const attachLink = async () => {
    const value = window.prompt("Ссылка на доказательство: коммит, merge request, документ или портфолио")
    if (!value?.trim()) return
    setAttaching(true)
    try {
      await attachInterviewLink(runId, value.trim(), "")
      await retry()
      toast.success("Ссылка добавлена в интервью")
    } catch (cause) {
      toast.error("Не удалось добавить ссылку", {
        description: cause instanceof Error ? cause.message : "Проверьте ссылку.",
      })
    } finally {
      setAttaching(false)
    }
  }

  if (loading) {
    return (
      <div className="space-y-2">
        <Skeleton className="h-16 w-2/3" />
        <Skeleton className="ml-auto h-16 w-1/2" />
        <Skeleton className="h-16 w-2/3" />
      </div>
    )
  }

  if (loadError && messages.length === 0) {
    return (
      <div className="space-y-3 rounded-xl border border-destructive/50 p-8 text-center">
        <p className="text-sm text-destructive">{loadError}</p>
        <Button variant="outline" onClick={() => void retry()}>
          <RefreshCwIcon className="size-4" />
          Повторить
        </Button>
      </div>
    )
  }

  return (
    <div className="space-y-3">
      {progress && (
        <div className="rounded-lg border bg-muted/30 px-3 py-2 text-xs">
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
            <span className="font-medium">
              Вопросы: {progress.asked} из {progress.total}
            </span>
            <span className="text-muted-foreground">Осталось: {progress.remaining}</span>
            {progress.average_quality > 0 && (
              <span className="text-muted-foreground">Качество ответов: {progress.average_quality}/100</span>
            )}
          </div>
          {progress.uncovered_topics?.length > 0 && (
            <p className="mt-1 text-muted-foreground">
              Не затронуты: <span className="text-foreground">{progress.uncovered_topics.join(", ")}</span>
            </p>
          )}
          {progress.contradictions && progress.contradictions.length > 0 && (
            <p className="mt-1 text-warning">
              Расхождение с GitHub: {progress.contradictions.join(", ")}. Агент уточнит это в следующем вопросе.
            </p>
          )}
        </div>
      )}
      {loadError && messages.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 rounded-lg border border-warning/50 bg-warning/10 px-3 py-2 text-xs">
          <InfoIcon className="size-3.5 shrink-0" />
          <span>Интервью было прервано. История сохранена, можно продолжить с последнего вопроса.</span>
          <Button variant="outline" size="sm" className="ml-auto h-6 px-2 text-xs" onClick={() => void retry()}>
            <RefreshCwIcon className="size-3" />
            Продолжить
          </Button>
          <Link className="text-xs underline underline-offset-2" to="/research/new">
            Новое интервью
          </Link>
        </div>
      )}
      {runStatus === "failed" || runStatus === "degraded" ? (
        <div className="flex items-center gap-2 rounded-lg border border-destructive/50 px-3 py-2 text-xs text-destructive">
          <WifiOffIcon className="size-3.5 shrink-0" />
          Исследование завершилось ошибкой - чат закрыт.
        </div>
      ) : chatClosed ? (
        <div className="flex flex-wrap items-center gap-2 rounded-lg border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
          <InfoIcon className="size-3.5 shrink-0" />
          <span>
            Интервью завершено - чат закрыт. История сохранена, можно вернуться и перечитать.
          </span>
          {messages.length > 0 && (
            <Button
              variant="outline"
              size="sm"
              className="ml-auto h-6 px-2 text-xs"
              onClick={() =>
                downloadText(
                  "interview.md",
                  chatTranscriptToMarkdown(messages, "Интервью"),
                  "text/markdown;charset=utf-8",
                )
              }
            >
              <DownloadIcon className="size-3" />
              Скачать транскрипт
            </Button>
          )}
        </div>
      ) : interviewFinished ? (
        <div className="flex items-center gap-2 rounded-lg border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
          <InfoIcon className="size-3.5 shrink-0" />
          Интервью завершено - агент-аналитик строит дерево.
        </div>
      ) : (
        streamState !== "open" && (
          <div className="flex items-center gap-2 rounded-lg border border-destructive/50 px-3 py-2 text-xs text-destructive">
            <WifiOffIcon className="size-3.5 shrink-0" />
            {streamState === "reconnecting"
              ? "Соединение потеряно - переподключаюсь…"
              : "SSE-поток недоступен: новые сообщения приходят опросом раз в 5 секунд."}
            {streamState === "failed" && (
              <Button
                variant="outline"
                size="sm"
                className="ml-auto h-6 px-2 text-xs"
                onClick={() => void retry()}
              >
                <RefreshCwIcon className="size-3" />
                Обновить
              </Button>
            )}
          </div>
        )
      )}

      <div className="max-h-[480px] space-y-3 overflow-y-auto rounded-xl border p-4">
        {messages.length === 0 && !thinking && (
          <p className="py-8 text-center text-sm text-muted-foreground">
            Агент ещё не задал первый вопрос - он появится здесь.
          </p>
        )}
        {messages.map((message) => (
          <div
            key={message.id}
            id={`chat-message-${message.id}`}
            className={`rounded-lg transition-shadow ${
              highlightedId === message.id ? "ring-2 ring-primary ring-offset-2 ring-offset-background" : ""
            } ${message.role === "user" ? "flex justify-end" : "flex justify-start"}`}
          >
            <div
              className={
                message.role === "user"
                  ? "max-w-[80%] rounded-lg bg-primary px-3 py-2 text-primary-foreground"
                  : "max-w-[80%] rounded-lg bg-muted px-3 py-2 text-foreground"
              }
            >
              <p className="whitespace-pre-wrap text-sm">{renderContent(message)}</p>
              {message.citations && message.citations.length > 0 && (
                <div className="mt-2 space-y-1 border-t border-border/60 pt-2">
                  <p className="text-xs font-medium text-muted-foreground">
                    На чём основан ответ
                  </p>
                  <ul className="space-y-1">
                    {message.citations.map((citation, index) => (
                      <li key={`${citation.quote}-${index}`} className="text-xs">
                        <span className="text-muted-foreground">{citation.title}: </span>
                        <span className="italic">«{citation.quote}»</span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </div>
          </div>
        ))}
        {thinking && (
          <div className="flex justify-start">
            <div className="flex items-center gap-1.5 rounded-lg bg-muted px-3 py-2">
              <span className="size-1.5 animate-bounce rounded-full bg-muted-foreground" />
              <span className="size-1.5 animate-bounce rounded-full bg-muted-foreground [animation-delay:120ms]" />
              <span className="size-1.5 animate-bounce rounded-full bg-muted-foreground [animation-delay:240ms]" />
              <span className="ml-1 text-xs text-muted-foreground">агент думает…</span>
            </div>
          </div>
        )}
        <div ref={bottomRef} />
      </div>

      {readOnly ? (
        <p className="rounded-lg border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
          Режим просмотра: отправка сообщений недоступна.
        </p>
      ) : (
      <div className="flex items-end gap-2">
        <input
          ref={fileInput}
          type="file"
          accept=".pdf,.docx,.txt,.md"
          className="hidden"
          onChange={(event) => void attachFile(event.target.files?.[0])}
        />
        <Button
          type="button"
          variant="outline"
          size="icon"
          className="shrink-0"
          onClick={() => void attachLink()}
          disabled={attaching || chatClosed}
          aria-label="Прикрепить ссылку"
          title="Прикрепить ссылку на доказательство"
        >
          <LinkIcon className="size-4" />
        </Button>
        <Button
          type="button"
          variant="outline"
          size="icon"
          className="shrink-0"
          onClick={() => fileInput.current?.click()}
          disabled={attaching || chatClosed}
          aria-label="Прикрепить доказательство"
          title="Прикрепить доказательство: PDF, DOCX, TXT или MD"
        >
          {attaching ? <Loader2Icon className="size-4 animate-spin" /> : <PaperclipIcon className="size-4" />}
        </Button>
        <Textarea
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={onKeyDown}
          rows={2}
          disabled={chatClosed}
          placeholder={
            chatClosed
              ? "Интервью завершено - новые ответы не отправляются"
              : "Ответ на вопрос агента… Enter - отправить, Shift+Enter - новая строка"
          }
        />
        <Button
          onClick={() => void send()}
          disabled={!draft.trim() || sending || chatClosed}
          className="shrink-0"
        >
          {sending ? <Loader2Icon className="size-4 animate-spin" /> : <SendIcon className="size-4" />}
          Отправить
        </Button>
      </div>
      )}
    </div>
  )
}
