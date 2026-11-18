/**
 * @file ChatTab.tsx
 * @description Вкладка чата: история интервью, отправка ответов и просмотр после завершения.
 */

import { useEffect, useRef, useState, type KeyboardEvent } from "react"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import { type Run, type RunStatus } from "@/lib/api/client"
import { downloadText } from "@/lib/export-tree"
import { chatTranscriptToMarkdown } from "@/lib/export-chat"
import { useChatStream } from "@/hooks/use-chat-stream"
import {
  DownloadIcon,
  InfoIcon,
  Loader2Icon,
  RefreshCwIcon,
  SendIcon,
  WifiOffIcon,
} from "lucide-react"

interface ChatTabProps {
  runId: string
  active: boolean
  runStatus?: RunStatus
  stage?: Run["stage"]
  /** Цитата, к которой нужно прокрутить историю (переход из диалога обоснования). */
  focus?: { refId?: string; quote: string } | null
}

export function ChatTab({ runId, active, runStatus, stage, focus }: ChatTabProps) {
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
  const appliedFocus = useRef<string | null>(null)

  const renderContent = (message: (typeof messages)[number]) => {
    if (highlightedId !== message.id || !focus?.quote) return message.content
    const index = message.content.toLowerCase().indexOf(focus.quote.toLowerCase())
    if (index < 0) return message.content
    return (
      <>
        {message.content.slice(0, index)}
        <mark className="rounded bg-warning/30 px-0.5 text-inherit">
          {message.content.slice(index, index + focus.quote.length)}
        </mark>
        {message.content.slice(index + focus.quote.length)}
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
      {runStatus === "failed" || runStatus === "degraded" ? (
        <div className="flex items-center gap-2 rounded-lg border border-destructive/50 px-3 py-2 text-xs text-destructive">
          <WifiOffIcon className="size-3.5 shrink-0" />
          Прогон завершился ошибкой - чат закрыт.
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

      <div className="flex items-end gap-2">
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
    </div>
  )
}
