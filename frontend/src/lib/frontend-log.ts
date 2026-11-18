/**
 * @file frontend-log.ts
 * @description Сбор ошибок фронтенда в файл без элементов интерфейса.
 *
 * В dev-режиме события отправляются на `/__frontend-log`, а Vite-плагин
 * дописывает их в `frontend/logs/frontend.log`. В production вызовы
 * превращаются в no-op, чтобы не шуметь в боевом окружении.
 */

export type FrontendLogLevel = "debug" | "info" | "warn" | "error"

const ENDPOINT = "/__frontend-log"

function send(level: FrontendLogLevel, message: string, context?: Record<string, unknown>) {
  if (import.meta.env.PROD || import.meta.env.MODE === "test") return
  const payload = {
    time: new Date().toISOString(),
    level,
    message,
    context,
    url: typeof window !== "undefined" ? window.location.href : "",
  }

  if (level === "error") console.error(`[frontend] ${message}`, context ?? "")
  else if (level === "warn") console.warn(`[frontend] ${message}`, context ?? "")

  try {
    void fetch(ENDPOINT, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
      keepalive: true,
    }).catch(() => undefined)
  } catch {
    // Сбор логов не должен ломать приложение.
  }
}

export const frontendLog = {
  debug: (message: string, context?: Record<string, unknown>) => send("debug", message, context),
  info: (message: string, context?: Record<string, unknown>) => send("info", message, context),
  warn: (message: string, context?: Record<string, unknown>) => send("warn", message, context),
  error: (message: string, context?: Record<string, unknown>) => send("error", message, context),
}
let installed = false

/** Подписывается на глобальные ошибки окна и необработанные промисы. */
export function installFrontendLogHandlers() {
  if (installed || typeof window === "undefined") return
  installed = true

  window.addEventListener("error", (event) => {
    frontendLog.error("uncaught error", {
      message: event.message,
      source: event.filename,
      line: event.lineno,
      column: event.colno,
    })
  })

  window.addEventListener("unhandledrejection", (event) => {
    frontendLog.error("unhandled rejection", { reason: String(event.reason) })
  })
}
