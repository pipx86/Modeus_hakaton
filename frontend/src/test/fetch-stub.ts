/**
 * @file fetch-stub.ts
 * @description Подмена fetch в тестах: детерминированные ответы API.
 */

import { vi } from "vitest"

export interface StubResponse {
  status: number
  body?: unknown
  /** Дополнительные заголовки ответа: Content-Type и Content-Disposition у файлов. */
  headers?: Record<string, string>
}

export type FetchHandler = (
  url: string,
  init: RequestInit,
) => StubResponse | Promise<StubResponse>

export interface NormalizedCall {
  url: string
  init: RequestInit
}

function toResponse({ status, body, headers }: StubResponse) {
  const responseHeaders = new Headers({ "content-type": "application/json", ...headers })
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: responseHeaders,
    json: async () => body,
    text: async () => {
      if (typeof body === "string") return body
      if (body instanceof Blob) return body.text()
      return JSON.stringify(body ?? "")
    },
    // Ответ-файл (например, DOCX) отдаётся тем же стабом, что и JSON.
    blob: async () =>
      body instanceof Blob
        ? body
        : new Blob([JSON.stringify(body ?? "")], {
            type: responseHeaders.get("content-type") ?? "",
          }),
  } as unknown as Response
}

async function normalizeCall(input: unknown, init?: RequestInit): Promise<NormalizedCall> {
  if (typeof input === "string") return { url: input, init: init ?? {} }
  if (input instanceof URL) return { url: input.toString(), init: init ?? {} }
  if (typeof Request !== "undefined" && input instanceof Request) {
    let body: string | undefined
    try {
      body = await input.clone().text()
    } catch {
      body = undefined
    }
    return {
      url: input.url,
      init: {
        ...init,
        method: input.method,
        body,
        headers: Object.fromEntries(input.headers.entries()),
      },
    }
  }
  return { url: String(input), init: init ?? {} }
}

const calls: NormalizedCall[] = []

let currentHandler: FetchHandler = () => ({
  status: 500,
  body: { code: "STUB_NO_HANDLER", message: "fetch-stub: handler не задан" },
})

const spy = vi.fn(async (input: unknown, init?: RequestInit) => {
  const call = await normalizeCall(input, init)
  calls.push(call)
  return toResponse(await currentHandler(call.url, call.init))
})

// ВАЖНО: модуль импортируется раньше компонентов в тестовых файлах
vi.stubGlobal("fetch", spy)

export function setFetchHandler(handler: FetchHandler) {
  currentHandler = handler
  calls.length = 0
  spy.mockClear()
  return spy
}

export function fetchCalls(): NormalizedCall[] {
  return calls
}