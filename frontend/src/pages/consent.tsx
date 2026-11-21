/**
 * @file consent.tsx
 * @description Публичная страница согласия на обработку персональных данных:
 * текст документа, статус текущего пользователя и принятие согласия.
 */

import { useEffect, useState } from "react"
import { Link, useNavigate } from "react-router-dom"
import {
  AlertTriangleIcon,
  ArrowLeftIcon,
  CheckCircle2Icon,
  Loader2Icon,
  ShieldCheckIcon,
} from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  ApiRequestError,
  acceptConsent,
  getConsentDocument,
  getConsentStatus,
  type ConsentDocument,
  type ConsentStatus,
} from "@/lib/api/client"
import { getValidToken } from "@/lib/auth"
import { notifyConsentAccepted } from "@/lib/consent"

const HEADING = "Согласие на обработку персональных данных"
const FALLBACK_VERSION = "v1"

/** Типовой текст на случай, если документ согласия недоступен. */
const FALLBACK_TEXT = [
  "Обработка персональных данных выполняется для регистрации и работы в сервисе анализа профессиональной деятельности.",
  "Обрабатываются адрес электронной почты, имя, ответы интервью и материалы, которые пользователь загружает сам.",
  "Данные хранятся в защищённом виде и не передаются третьим лицам, кроме случаев, предусмотренных законом.",
  "Согласие можно отозвать: после отзыва запуск новых исследований станет недоступен.",
  "Продолжая работу, пользователь подтверждает, что ознакомился с этим текстом.",
].join("\n\n")

/** Понятное сообщение об ошибке принятия согласия. */
function describeAcceptError(cause: unknown): string {
  if (cause instanceof ApiRequestError) {
    if (cause.status === 0) return "Сервер недоступен: проверьте соединение."
    return cause.message
  }
  return "Не удалось принять согласие. Попробуйте ещё раз."
}

/**
 * Показывает документ согласия и статус пользователя. Принятие доступно только
 * после входа: публичная страница сама по себе ничего не меняет.
 */
export function Consent() {
  const navigate = useNavigate()
  const [authenticated] = useState(() => getValidToken() !== null)
  const [consentDocument, setConsentDocument] = useState<ConsentDocument | null>(null)
  const [status, setStatus] = useState<ConsentStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [documentFailed, setDocumentFailed] = useState(false)
  const [statusFailed, setStatusFailed] = useState(false)
  const [accepting, setAccepting] = useState(false)
  const [acceptError, setAcceptError] = useState<string | null>(null)

  useEffect(() => {
    let active = true
    const load = async () => {
      const [documentResult, statusResult] = await Promise.allSettled([
        getConsentDocument(),
        authenticated ? getConsentStatus() : Promise.resolve(null),
      ])
      if (!active) return
      if (documentResult.status === "fulfilled") setConsentDocument(documentResult.value)
      else setDocumentFailed(true)
      if (statusResult.status === "fulfilled") setStatus(statusResult.value)
      else if (authenticated) setStatusFailed(true)
      setLoading(false)
    }
    // Данные загружаются асинхронно; состояние обновляется после ответа API.
    void load()
    return () => {
      active = false
    }
  }, [authenticated])

  const version =
    consentDocument?.version?.trim() || status?.document_version?.trim() || FALLBACK_VERSION
  const text = consentDocument?.text?.trim() ? consentDocument.text : FALLBACK_TEXT
  const documentTitle =
    consentDocument?.title?.trim() && consentDocument.title !== HEADING
      ? consentDocument.title
      : "Пожалуйста, ознакомьтесь с документом."

  const accept = async () => {
    setAccepting(true)
    setAcceptError(null)
    try {
      const nextStatus = await acceptConsent(version)
      setStatus(nextStatus)
      setStatusFailed(false)
      notifyConsentAccepted()
      toast.success("Согласие получено")
    } catch (cause) {
      const message = describeAcceptError(cause)
      setAcceptError(message)
      toast.error("Не удалось принять согласие", { description: message })
    } finally {
      setAccepting(false)
    }
  }

  const goBack = () => {
    const historyState = window.history.state as { idx?: unknown } | null
    if (typeof historyState?.idx === "number" && historyState.idx > 0) navigate(-1)
    else navigate("/")
  }

  return (
    <div className="mx-auto w-full max-w-3xl space-y-5">
      <Button variant="ghost" size="sm" className="-ml-2 w-fit" onClick={goBack}>
        <ArrowLeftIcon className="size-4" />
        Назад
      </Button>

      <div className="space-y-1">
        <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
          <ShieldCheckIcon className="size-5 shrink-0 text-primary" />
          {HEADING}
        </h1>
        <p className="text-sm text-muted-foreground">Версия документа: {version}</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Текст согласия</CardTitle>
          <CardDescription>{documentTitle}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {documentFailed && !loading && (
            <p className="flex items-start gap-2 rounded-lg border border-warning/40 bg-warning/10 p-3 text-sm">
              <AlertTriangleIcon className="mt-0.5 size-4 shrink-0 text-warning" />
              Не удалось загрузить текст с сервера. Показан типовой текст согласия.
            </p>
          )}
          {loading ? (
            <p className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2Icon className="size-4 animate-spin" />
              Загружаем текст согласия…
            </p>
          ) : (
            <div className="whitespace-pre-line text-sm leading-relaxed">{text}</div>
          )}
        </CardContent>
      </Card>

      {authenticated && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Статус согласия</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {loading ? (
              <p className="flex items-center gap-2 text-sm text-muted-foreground">
                <Loader2Icon className="size-4 animate-spin" />
                Проверяем статус согласия…
              </p>
            ) : status?.accepted ? (
              <p className="flex items-center gap-2 text-sm font-medium text-success">
                <CheckCircle2Icon className="size-4 shrink-0" />
                Согласие получено
              </p>
            ) : (
              <>
                <p className="text-sm text-muted-foreground">
                  {statusFailed
                    ? "Не удалось проверить статус согласия. Можно принять его ещё раз."
                    : "Согласие ещё не принято. Без него нельзя запускать исследования."}
                </p>
                <Button onClick={() => void accept()} disabled={accepting}>
                  {accepting && <Loader2Icon className="size-4 animate-spin" />}
                  {accepting ? "Сохраняем…" : "Принять"}
                </Button>
                {acceptError && (
                  <p role="alert" className="text-sm text-destructive">
                    {acceptError}
                  </p>
                )}
              </>
            )}
          </CardContent>
        </Card>
      )}

      {!authenticated && (
        <p className="text-sm text-muted-foreground">
          Чтобы принять согласие,{" "}
          <Link to="/login" className="text-primary underline underline-offset-4">
            войдите в аккаунт
          </Link>
          .
        </p>
      )}
    </div>
  )
}
