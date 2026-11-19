/**
 * @file auth.ts
 * @description Работа с локальной сессией по JWT: декодирование payload,
 * проверка срока действия и хранение токена в localStorage.
 *
 * Токен выдаёт API при регистрации или входе. Фронтенд передаёт его в Authorization.
 */

const STORAGE_KEY = "modeus.jwt"
const AUTH_CHANGED = "modeus:auth-changed"

export function subscribeAuth(listener: () => void) {
  const storageChanged = (event: StorageEvent) => {
    if (event.key === STORAGE_KEY || event.key === null) listener()
  }
  window.addEventListener(AUTH_CHANGED, listener)
  window.addEventListener("storage", storageChanged)
  return () => {
    window.removeEventListener(AUTH_CHANGED, listener)
    window.removeEventListener("storage", storageChanged)
  }
}

export interface TokenPayload {
  sub?: string
  role?: string
  iat?: number
  exp?: number
}

/** Декодирует payload JWT без проверки подписи (подпись проверяет бэкенд). */
export function decodeToken(token: string): TokenPayload | null {
  const parts = token.trim().split(".")
  if (parts.length !== 3) return null
  try {
    const base64 = parts[1].replace(/-/g, "+").replace(/_/g, "/")
    const padded = base64.padEnd(base64.length + ((4 - (base64.length % 4)) % 4), "=")
    const decoded = JSON.parse(window.atob(padded)) as TokenPayload
    if (!decoded || typeof decoded !== "object") return null
    return decoded
  } catch {
    return null
  }
}

export function isTokenExpired(payload: TokenPayload | null): boolean {
  if (!payload?.exp) return false
  return payload.exp * 1000 <= Date.now()
}

export function getStoredToken(): string | null {
  try {
    return window.localStorage.getItem(STORAGE_KEY)
  } catch {
    return null
  }
}

/** Возвращает токен только если он есть, декодируется и не истёк. */
export function getValidToken(): string | null {
  const token = getStoredToken()
  if (!token) return null
  const payload = decodeToken(token)
  if (!payload || isTokenExpired(payload)) return null
  return token
}

export function storeToken(token: string) {
  window.localStorage.setItem(STORAGE_KEY, token.trim())
  window.dispatchEvent(new Event(AUTH_CHANGED))
}

export function clearToken() {
  window.localStorage.removeItem(STORAGE_KEY)
  window.dispatchEvent(new Event(AUTH_CHANGED))
}

/** Роль текущей сессии или null, если токена нет/он просрочен. */
export function getCurrentRole(): string | null {
  const token = getValidToken()
  if (!token) return null
  return decodeToken(token)?.role ?? null
}

/** Идентификатор текущего аккаунта из `sub` JWT. */
export function getCurrentUserID(): string | null {
  const token = getValidToken()
  if (!token) return null
  return decodeToken(token)?.sub ?? null
}
