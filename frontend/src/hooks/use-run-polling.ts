/**
 * @file use-run-polling.ts
 * @description Загрузка прогона и поллинг статуса, пока он выполняется.
 */

import { useCallback, useEffect, useRef, useState } from "react"
import { apiClient, type Run } from "@/lib/api/client"

interface UseRunPollingOptions {
  /** Вызывается при смене статуса прогона (кроме первой загрузки). */
  onStatusChange?: (previous: Run["status"], next: Run["status"]) => void
}

/**
 * Загружает прогон по id и обновляет его раз в 5 секунд, пока он активен.
 *
 * Возвращает `reload` для ручного повтора и `isActive`, по которому страница
 * понимает, нужно ли показывать прогресс.
 */
export function useRunPolling(runId: string, options: UseRunPollingOptions = {}) {
  const [run, setRun] = useState<Run | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const previousStatus = useRef<Run["status"] | undefined>(undefined)
  const statusCallback = useRef(options.onStatusChange)
  statusCallback.current = options.onStatusChange

  const loadRun = useCallback(async () => {
    try {
      const { data } = await apiClient.GET("/runs/{id}", {
        params: { path: { id: runId } },
      })
      if (data) setRun(data)
      setError(null)
    } catch {
      setError("Бэкенд недоступен по адресу из VITE_API_URL")
    } finally {
      setLoading(false)
    }
  }, [runId])

  useEffect(() => {
    // Первичная загрузка прогона асинхронная; состояние обновит ответ API.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void loadRun()
  }, [loadRun])

  const isActive = run?.status === "pending" || run?.status === "running"
  useEffect(() => {
    if (!isActive) return
    const timer = setInterval(() => void loadRun(), 5000)
    return () => clearInterval(timer)
  }, [isActive, loadRun])

  useEffect(() => {
    const previous = previousStatus.current
    previousStatus.current = run?.status
    if (!previous || previous === run?.status) return
    statusCallback.current?.(previous, run.status)
  }, [run?.status])

  return { run, loading, error, isActive, reload: loadRun }
}
