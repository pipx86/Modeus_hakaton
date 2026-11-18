import { useEffect, useState } from "react"
import { Link } from "react-router-dom"
import { ShieldAlertIcon, Trash2Icon, UsersIcon } from "lucide-react"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { StatusBadge } from "@/components/status-badge"
import { apiClient, type Run } from "@/lib/api/client"
import { decodeToken, getCurrentRole, getValidToken } from "@/lib/auth"

interface DemoUser {
  subject: string
  role: string
  read_only?: boolean
}

const ROLE_LABEL: Record<string, string> = {
  admin: "Администратор",
  guest: "Гость",
  teacher: "Преподаватель",
  rop: "РОП",
  student: "Студент",
}

/** Админка: пользователи, исследования и состояние сервиса. Доступна роли admin. */
export function Admin() {
  const role = getCurrentRole()
  const isAdmin = role === "admin"
  const [users, setUsers] = useState<DemoUser[]>([])
  const [runs, setRuns] = useState<Run[]>([])
  const [health, setHealth] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [deletingId, setDeletingId] = useState<string | null>(null)

  const session = (() => {
    const token = getValidToken()
    return token ? decodeToken(token) : null
  })()

  useEffect(() => {
    if (!isAdmin) {
      setLoading(false)
      return
    }
    let cancelled = false
    const load = async () => {
      setLoading(true)
      const [usersResponse, runsResponse, healthResponse] = await Promise.all([
        apiClient.GET("/users"),
        apiClient.GET("/runs"),
        apiClient.GET("/health"),
      ])
      if (cancelled) return
      if (usersResponse.data) setUsers(usersResponse.data)
      if (runsResponse.data) setRuns(runsResponse.data)
      setHealth(healthResponse.data?.status ?? "недоступен")
      setLoading(false)
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [isAdmin])

  const removeRun = async (run: Run) => {
    setDeletingId(run.id)
    try {
      const { error } = await apiClient.DELETE("/runs/{id}", { params: { path: { id: run.id } } })
      if (error) {
        toast.error("Не удалось удалить исследование", { description: error.message })
        return
      }
      setRuns((prev) => prev.filter((item) => item.id !== run.id))
      toast.success("Исследование удалено")
    } catch {
      toast.error("Сервер недоступен", { description: "Исследование не удалено." })
    } finally {
      setDeletingId(null)
    }
  }

  if (!isAdmin) {
    return (
      <div className="mx-auto max-w-xl">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <ShieldAlertIcon className="size-5 text-destructive" />
              Доступ только для администратора
            </CardTitle>
            <CardDescription>
              Текущая роль: {role ? (ROLE_LABEL[role] ?? role) : "не авторизован"}. Войдите с ролью
              admin, чтобы открыть админку.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button asChild>
              <Link to="/login">Перейти ко входу</Link>
            </Button>
          </CardContent>
        </Card>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <div className="space-y-1">
        <h1 className="text-3xl font-semibold tracking-tight">Админка</h1>
        <p className="text-sm text-muted-foreground">
          {session?.sub ?? "admin"} - управление пользователями и исследованиями.
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <UsersIcon className="size-4 shrink-0" />
            Пользователи
          </CardTitle>
          <CardDescription>Демонстрационные учётные записи и их права.</CardDescription>
        </CardHeader>
        <CardContent>
          {loading ? (
            <div className="space-y-2">
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
            </div>
          ) : (
            <ul className="divide-y">
              {users.map((user) => (
                <li key={user.subject} className="flex flex-wrap items-center gap-3 py-3">
                  <span className="font-medium">{user.subject}</span>
                  <Badge variant={user.role === "admin" ? "default" : "secondary"}>
                    {ROLE_LABEL[user.role] ?? user.role}
                  </Badge>
                  {user.read_only && (
                    <span className="text-xs text-muted-foreground">только чтение</span>
                  )}
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Исследования</CardTitle>
          <CardDescription>Всего: {runs.length}. Удаление необратимо.</CardDescription>
        </CardHeader>
        <CardContent>
          {runs.length === 0 ? (
            <p className="text-sm text-muted-foreground">Исследований нет.</p>
          ) : (
            <ul className="divide-y">
              {runs.map((run) => (
                <li key={run.id} className="flex items-center gap-3 py-3">
                  <div className="min-w-0 flex-1">
                    <Link to={`/research/${run.id}`} className="block truncate font-medium hover:underline">
                      {run.profession}
                    </Link>
                  </div>
                  <StatusBadge status={run.status} />
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={`Удалить исследование ${run.profession}`}
                    className="shrink-0 text-muted-foreground hover:text-destructive"
                    disabled={deletingId === run.id}
                    onClick={() => void removeRun(run)}
                  >
                    <Trash2Icon className="size-4" />
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Состояние сервиса</CardTitle>
        </CardHeader>
        <CardContent className="text-sm">
          API: <span className="font-medium">{health ?? "проверяется…"}</span>
        </CardContent>
      </Card>
    </div>
  )
}
