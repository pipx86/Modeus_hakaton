/**
 * @file require-auth.tsx
 * @description Защита маршрутов: редирект на вход, если нет валидного JWT.
 */

import type { ReactNode } from "react"
import { Navigate, useLocation } from "react-router-dom"
import { getValidToken } from "@/lib/auth"

/**
 * Обязательный вход: без действующего JWT любой защищённый маршрут
 * перенаправляет на /login и запоминает, куда вернуться после входа.
 */
export function RequireAuth({ children }: { children: ReactNode }) {
  const location = useLocation()

  if (!getValidToken()) {
    return (
      <Navigate
        to="/login"
        replace
        state={{ from: `${location.pathname}${location.search}` }}
      />
    )
  }

  return <>{children}</>
}
