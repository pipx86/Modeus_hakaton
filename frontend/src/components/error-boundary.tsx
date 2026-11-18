/**
 * @file error-boundary.tsx
 * @description Граница ошибок рендера: понятный экран вместо белого полотна и повторный рендер.
 */

import { Component, type ErrorInfo, type ReactNode } from "react"
import { AlertCircleIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { frontendLog } from "@/lib/frontend-log"

interface ErrorBoundaryProps {
  children: ReactNode
  /** `page` - на весь экран, `inline` - внутри Layout вокруг маршрута. */
  variant?: "page" | "inline"
}

interface ErrorBoundaryState {
  error: Error | null
}

/**
 * Ловит ошибки рендера во всём дереве приложения и показывает понятный экран
 * вместо белого полотна. Кнопка «Попробовать снова» сбрасывает состояние,
 * «Обновить страницу» перезагружает приложение целиком.
 */
export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    frontendLog.error("render error", {
      message: error.message,
      stack: error.stack,
      componentStack: info.componentStack,
    })
    console.error("Необработанная ошибка интерфейса", error, info)
  }

  render() {
    if (!this.state.error) return this.props.children

    const inline = this.props.variant === "inline"

    return (
      <div
        className={
          inline
            ? "flex min-h-[calc(100vh-7rem)] w-full items-center justify-center p-6"
            : "flex min-h-dvh items-center justify-center p-6"
        }
      >
        <div className="flex w-full max-w-md flex-col items-center gap-4 rounded-xl border border-destructive/50 p-8 text-center">
          <AlertCircleIcon className="size-8 text-destructive" />
          <div className="space-y-1">
            <p className="text-lg font-semibold">Что-то пошло не так</p>
            <p className="text-sm text-muted-foreground">
              Интерфейс столкнулся с ошибкой. Данные не потеряны - попробуйте ещё раз или обновите
              страницу.
            </p>
          </div>
          <div className="flex flex-wrap justify-center gap-2">
            <Button variant="outline" onClick={() => this.setState({ error: null })}>
              Попробовать снова
            </Button>
            <Button onClick={() => window.location.reload()}>Обновить страницу</Button>
          </div>
        </div>
      </div>
    )
  }
}
