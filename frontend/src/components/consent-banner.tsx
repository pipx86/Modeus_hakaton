/**
 * @file consent-banner.tsx
 * @description Напоминание принять согласие на обработку персональных данных.
 */

import { useEffect, useState, useSyncExternalStore } from "react"
import { Link, useLocation } from "react-router-dom"
import { AlertTriangleIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { getConsentStatus } from "@/lib/api/client"
import { getValidToken, subscribeAuth } from "@/lib/auth"
import { subscribeConsentAccepted } from "@/lib/consent"

interface ConsentBannerState {
  token: string
  accepted: boolean
}

/**
 * Баннер для авторизованного пользователя без согласия. Показывается поверх
 * любой страницы приложения, кроме самой `/consent`, и не мешает работе, если
 * проверить статус не удалось: тогда баннер просто скрыт.
 */
export function ConsentBanner() {
  const token = useSyncExternalStore(subscribeAuth, getValidToken)
  const { pathname } = useLocation()
  const [state, setState] = useState<ConsentBannerState | null>(null)

  useEffect(() => {
    if (!token) return
    let active = true
    let acceptedByEvent = false
    const unsubscribe = subscribeConsentAccepted(() => {
      acceptedByEvent = true
      if (active) setState({ token, accepted: true })
    })
    getConsentStatus()
      .then((status) => {
        if (active && !acceptedByEvent) setState({ token, accepted: status.accepted })
      })
      .catch(() => {
        // Статус недоступен: не показываем ложное напоминание.
      })
    return () => {
      active = false
      unsubscribe()
    }
  }, [token])

  const missingConsent =
    Boolean(token) && state?.token === token && state?.accepted === false

  if (!missingConsent || pathname === "/consent") return null

  return (
    <section
      aria-label="Согласие на обработку персональных данных"
      className="flex flex-col gap-3 rounded-xl border border-warning/40 bg-warning/10 p-4 text-sm sm:flex-row sm:items-center sm:justify-between"
    >
      <div className="flex items-start gap-2">
        <AlertTriangleIcon className="mt-0.5 size-4 shrink-0 text-warning" />
        <div className="space-y-0.5">
          <p className="font-medium">Нужно согласие на обработку персональных данных</p>
          <p className="text-muted-foreground">
            Без согласия нельзя запускать новые исследования.
          </p>
        </div>
      </div>
      <Button asChild size="sm" className="shrink-0">
        <Link to="/consent">Открыть и принять</Link>
      </Button>
    </section>
  )
}
