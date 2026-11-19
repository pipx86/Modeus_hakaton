/**
 * @file login.tsx
 * @description Вход и регистрация по email и паролю.
 */

import { useState, type FormEvent } from "react"
import { useLocation, useNavigate } from "react-router-dom"
import { KeyRoundIcon, LogOutIcon, ShieldCheckIcon, UserPlusIcon } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { apiBaseUrl } from "@/lib/api/client"
import { normalizeText } from "@/lib/text"
import {
  clearToken,
  decodeToken,
  getStoredToken,
  isTokenExpired,
  storeToken,
  type TokenPayload,
} from "@/lib/auth"

type AuthMode = "login" | "register"

interface AuthResponse {
  token: string
  user?: { id: string; email: string; display_name: string; role: string }
}

const USER_STORAGE_KEY = "modeus.user"

function readStoredUser(): AuthResponseUser | null {
  try {
    const raw = window.localStorage.getItem(USER_STORAGE_KEY)
    return raw ? (JSON.parse(raw) as AuthResponseUser) : null
  } catch {
    return null
  }
}

type AuthResponseUser = NonNullable<AuthResponse["user"]>

function formatExpiry(exp?: number): string {
  if (!exp) return "без срока"
  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(exp * 1000))
}

async function postAuth(path: string, body: Record<string, string>): Promise<AuthResponse> {
  const response = await fetch(`${apiBaseUrl}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
  const payload = (await response.json().catch(() => null)) as
    | (AuthResponse & { message?: string; code?: string })
    | null
  if (!response.ok) {
    throw new Error(payload?.message ?? `HTTP ${response.status}`)
  }
  if (!payload?.token) throw new Error("Сервер не вернул токен")
  return payload
}

export function Login() {
  const navigate = useNavigate()
  const location = useLocation()
  const from = (location.state as { from?: string } | null)?.from ?? "/"

  const [mode, setMode] = useState<AuthMode>("login")
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [displayName, setDisplayName] = useState("")
  const [confirmPassword, setConfirmPassword] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [profile, setProfile] = useState<AuthResponseUser | null>(() => readStoredUser())
  const [submitting, setSubmitting] = useState(false)
  const [session, setSession] = useState<TokenPayload | null>(() => {
    const stored = getStoredToken()
    if (!stored) return null
    const payload = decodeToken(stored)
    return payload && !isTokenExpired(payload) ? payload : null
  })

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setError(null)
    if (mode === "register" && password !== confirmPassword) {
      setError("Пароли не совпадают")
      return
    }
    if (password.length < 8) {
      setError("Пароль должен содержать минимум 8 символов")
      return
    }
    setSubmitting(true)
    try {
      const result = await postAuth(
        mode === "login" ? "/auth/login" : "/auth/register",
        mode === "login"
          ? { email: email.trim(), password }
          : { email: email.trim(), password, display_name: displayName.trim() },
      )
      storeToken(result.token)
      if (result.user) {
        const profile = {
          ...result.user,
          display_name: normalizeText(result.user.display_name),
        }
        setProfile(profile)
        window.localStorage.setItem(USER_STORAGE_KEY, JSON.stringify(profile))
      }
      const payload = decodeToken(result.token)
      setSession(payload)
      setPassword("")
      setConfirmPassword("")
      setError(null)
      toast.success(mode === "login" ? "Вход выполнен" : "Аккаунт создан")
      navigate(from, { replace: true })
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Не удалось выполнить вход")
    } finally {
      setSubmitting(false)
    }
  }

  const logout = () => {
    clearToken()
    window.localStorage.removeItem(USER_STORAGE_KEY)
    setSession(null)
    setProfile(null)
    toast.success("Вы вышли из аккаунта")
  }

  const switchMode = (next: AuthMode) => {
    setMode(next)
    setError(null)
    setPassword("")
    setConfirmPassword("")
  }

  return (
    <div className="mx-auto flex min-h-[calc(100vh-7rem)] w-full max-w-lg flex-col justify-center py-6">
      <div className="space-y-4">
        <div className="space-y-1 text-center">
          <h1 className="text-3xl font-semibold tracking-tight">
            {session ? "Аккаунт" : mode === "login" ? "Вход" : "Регистрация"}
          </h1>
          <p className="text-sm text-muted-foreground">
            {session
              ? "Токен хранится в браузере и отправляется в API."
              : mode === "login"
                ? "Войдите по email и паролю."
                : "Создайте аккаунт: первый зарегистрированный пользователь становится администратором."}
          </p>
        </div>

        {session ? (
          <Card>
            <CardHeader className="text-center">
              <CardTitle className="flex items-center justify-center gap-2 text-base">
                <ShieldCheckIcon className="size-4 text-primary" />
                Вы вошли
              </CardTitle>
              <CardDescription>Роль: {session.role === "admin" ? "администратор" : "пользователь"}</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3 text-sm">
              <div className="grid gap-1">
                <span className="text-muted-foreground">Пользователь</span>
                <span className="font-medium">{profile?.display_name ?? profile?.email ?? session.sub ?? "не указан"}</span>
              </div>
              {profile?.email && (
                <div className="grid gap-1">
                  <span className="text-muted-foreground">Email</span>
                  <span className="font-medium">{profile.email}</span>
                </div>
              )}
              <div className="grid gap-1">
                <span className="text-muted-foreground">Действует до</span>
                <span className="font-medium">{formatExpiry(session.exp)}</span>
              </div>
              <Button variant="outline" onClick={logout}>
                <LogOutIcon className="size-4" />
                Выйти
              </Button>
            </CardContent>
          </Card>
        ) : (
          <Card>
            <CardHeader className="space-y-2">
              <div className="flex rounded-lg border p-1">
                <Button
                  type="button"
                  variant={mode === "login" ? "secondary" : "ghost"}
                  size="sm"
                  className="flex-1"
                  onClick={() => switchMode("login")}
                >
                  <KeyRoundIcon className="mr-1 size-4" />
                  Вход
                </Button>
                <Button
                  type="button"
                  variant={mode === "register" ? "secondary" : "ghost"}
                  size="sm"
                  className="flex-1"
                  onClick={() => switchMode("register")}
                >
                  <UserPlusIcon className="mr-1 size-4" />
                  Регистрация
                </Button>
              </div>
            </CardHeader>
            <CardContent>
              <form onSubmit={(event) => void submit(event)} className="space-y-4">
                {mode === "register" && (
                  <div className="space-y-2">
                    <Label htmlFor="display_name">Имя</Label>
                    <Input
                      id="display_name"
                      value={displayName}
                      onChange={(event) => setDisplayName(event.target.value)}
                      placeholder="Как к вам обращаться"
                      autoComplete="name"
                      required
                    />
                  </div>
                )}
                <div className="space-y-2">
                  <Label htmlFor="email">Email</Label>
                  <Input
                    id="email"
                    type="email"
                    value={email}
                    onChange={(event) => setEmail(event.target.value)}
                    placeholder="user@example.com"
                    autoComplete="email"
                    required
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="password">Пароль</Label>
                  <Input
                    id="password"
                    type="password"
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    placeholder="Минимум 8 символов"
                    autoComplete={mode === "login" ? "current-password" : "new-password"}
                    required
                  />
                </div>
                {mode === "register" && (
                  <div className="space-y-2">
                    <Label htmlFor="confirm_password">Повторите пароль</Label>
                    <Input
                      id="confirm_password"
                      type="password"
                      value={confirmPassword}
                      onChange={(event) => setConfirmPassword(event.target.value)}
                      autoComplete="new-password"
                      required
                    />
                  </div>
                )}
                {error && <p className="text-center text-sm text-destructive">{error}</p>}
                <Button type="submit" className="w-full" disabled={submitting}>
                  {submitting
                    ? mode === "login"
                      ? "Входим…"
                      : "Создаём…"
                    : mode === "login"
                      ? "Войти"
                      : "Зарегистрироваться"}
                </Button>
              </form>
            </CardContent>
          </Card>
        )}
      </div>
    </div>
  )
}
