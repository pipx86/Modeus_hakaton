import createClient from "openapi-fetch"
import type { paths, components } from "./schema"

// Префикс контракта живёт в servers.url, поэтому добавляем его к baseUrl
export const apiBaseUrl =
  (import.meta.env.VITE_API_URL || "http://localhost:8080") + "/api/v1"

export const apiClient = createClient<paths>({ baseUrl: apiBaseUrl })

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