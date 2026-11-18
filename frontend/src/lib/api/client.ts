import createClient from "openapi-fetch"
import { frontendLog } from "@/lib/frontend-log"
import type { paths, components } from "./schema"

// В dev запросы идут на тот же origin через Vite-прокси (он добавляет Authorization).
// В тестах jsdom требует абсолютный URL, поэтому там подставляется localhost.
const configuredBase = import.meta.env.VITE_API_URL
export const apiBaseUrl =
  configuredBase
    ? `${configuredBase}/api/v1`
    : import.meta.env.MODE === "test"
      ? "http://localhost:8080/api/v1"
      : "/api/v1"

export const apiToken = import.meta.env.VITE_API_TOKEN ?? ""

export const apiClient = createClient<paths>({ baseUrl: apiBaseUrl })

// Токен нужен, если фронтенд обращается к API напрямую (без прокси).
apiClient.use({
  onRequest({ request }) {
    if (apiToken) request.headers.set("Authorization", `Bearer ${apiToken}`)
    return request
  },
  onResponse({ request, response }) {
    if (response.status >= 500) {
      frontendLog.error("API 5xx", {
        method: request.method,
        url: request.url,
        status: response.status,
      })
    } else if (response.status >= 400) {
      frontendLog.warn("API 4xx", {
        method: request.method,
        url: request.url,
        status: response.status,
      })
    }
  },
  onError({ request, error }) {
    frontendLog.error("API request failed", {
      method: request.method,
      url: request.url,
      error: String(error),
    })
  },
})

export type HealthResponse = components["schemas"]["HealthResponse"]
export type ApiError = components["schemas"]["Error"]
export type NodeType = components["schemas"]["NodeType"]
export type SourceType = components["schemas"]["SourceType"]
export type RunStatus = components["schemas"]["RunStatus"]
export type Evidence = components["schemas"]["Evidence"]
export type ActivityNode = components["schemas"]["Node"]
export type Source = components["schemas"]["Source"]
export type Run = components["schemas"]["Run"]
export type ChatMessage = components["schemas"]["ChatMessage"]
export type RunEvent = components["schemas"]["RunEvent"]