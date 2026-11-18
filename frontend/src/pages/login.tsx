/**
 * @file login.tsx
 * @description Вход по JWT-токену, выданному администратором.
 */

import { useState, type FormEvent } from "react"
import { useLocation, useNavigate } from "react-router-dom"
import { KeyRoundIcon, LogOutIcon, ShieldCheckIcon } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Textarea } from "@/components/ui/textarea"
import {
  clearToken,
  decodeToken,
  getStoredToken,
  isTokenExpired,
  storeToken,
  type TokenPayload,
} from "@/lib/auth"

function formatExpiry(exp?: number): string {
  if (!exp) return "без срока"
  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(exp * 1000))
}

/** Страница входа: только JWT-токен, выданный администратором. */
export function Login() {
  const navigate = useNavigate()
  const location = useLocation()
  const from = (location.state as { from?: string } | null)?.from ?? "/"

  const [token, setToken] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [session, setSession] = useState<TokenPayload | null>(() => {
    const stored = getStoredToken()
    if (!stored) return null
    const payload = decodeToken(stored)
    return payload && !isTokenExpired(payload) ? payload : null
  })

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const raw = token.trim()
    const payload = decodeToken(raw)
    if (!payload) {
      setError("Это не похоже на JWT: ожидаются три части, разделённые точками.")
      return
    }
    if (isTokenExpired(payload)) {
      setError("Срок действия токена истёк. Выпустите новый через local-token.")
      return
    }
    storeToken(raw)
    setToken("")
    setError(null)
    setSession(payload)
    toast.success("Вход выполнен", { description: `Роль: ${payload.role ?? "не указана"}` })
    navigate(from, { replace: true })
  }

  const logout = () => {
    clearToken()
    setSession(null)
    toast.success("Вы вышли из аккаунта")
  }

  return (
    <div className="mx-auto flex min-h-[calc(100vh-7rem)] w-full max-w-lg flex-col justify-center py-6">
      <div className="space-y-4">
        <div className="space-y-1 text-center">
          <h1 className="text-3xl font-semibold tracking-tight">Вход</h1>
          <p className="text-sm text-muted-foreground">
            Авторизация по JWT-токену. Регистрация не поддерживается: токен выдаёт администратор.
          </p>
        </div>

        {session ? (
          <Card>
            <CardHeader className="text-center">
              <CardTitle className="flex items-center justify-center gap-2 text-base">
                <ShieldCheckIcon className="size-4 text-primary" />
                Вы вошли
              </CardTitle>
              <CardDescription>
                Токен хранится в браузере и отправляется в заголовке Authorization.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-3 text-sm">
              <div className="grid gap-1">
                <span className="text-muted-foreground">Пользователь</span>
                <span className="font-medium">{session.sub ?? "не указан"}</span>
              </div>
              <div className="grid gap-1">
                <span className="text-muted-foreground">Роль</span>
                <span className="font-medium">{session.role ?? "не указана"}</span>
              </div>
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
            <CardHeader className="flex min-h-20 flex-col items-center justify-center gap-2 py-5 text-center">
              <CardTitle className="flex items-center justify-center gap-2 text-base">
                <KeyRoundIcon className="size-4 text-primary" />
                JWT-токен
              </CardTitle>
            </CardHeader>
            <CardContent>
              <form onSubmit={submit} className="space-y-4">
                <div className="space-y-2">
                  <Textarea
                    id="jwt"
                    value={token}
                    onChange={(event) => setToken(event.target.value)}
                    placeholder="eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9…"
                    rows={2}
                    className="min-h-16 font-mono text-xs"
                  />
                  {error && <p className="text-center text-sm text-destructive">{error}</p>}
                  <pre className="overflow-x-auto rounded-lg border bg-muted/40 p-3 text-xs">
                    <code>go run ./cmd/local-token -role admin -ttl 24h</code>
                  </pre>
                </div>
                <Button type="submit" className="w-full" disabled={!token.trim()}>
                  Войти
                </Button>
              </form>
            </CardContent>
          </Card>
        )}
      </div>
    </div>
  )
}
