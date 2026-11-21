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
  // Номер последнего запроса: ответы устаревших запросов игнорируются.
  const seq = useRef(0)
  // Не запускаем новый поллинг-запрос, пока предыдущий ещё выполняется.
  const inFlight = useRef(false)

  useEffect(() => {
    statusCallback.current = options.onStatusChange
  }, [options.onStatusChange])

  const loadRun = useCallback(async () => {
    const id = ++seq.current
    inFlight.current = true
    try {
      const { data, error } = await apiClient.GET("/runs/{id}", {
        params: { path: { id: runId } },
      })
      if (id !== seq.current) return
      if (error) {
        setError(error.message || "Не удалось загрузить прогон.")
        return
      }
      if (data) {
        setRun(data)
        setError(null)
      }
    } catch {
      if (id !== seq.current) return
      setError("Бэкенд недоступен по адресу из VITE_API_URL")
    } finally {
      // Завершение устаревшего запроса не должно сбрасывать состояние нового.
      if (id === seq.current) {
        setLoading(false)
        inFlight.current = false
      }
    }
  }, [runId])

  useEffect(() => {
    // Смена runId: инвалидируем ответы старого прогона и сбрасываем состояние.
    seq.current += 1
    inFlight.current = false
    previousStatus.current = undefined
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setRun(null)
    setLoading(true)
    setError(null)
  }, [runId])

  useEffect(() => {
    // Первичная загрузка прогона асинхронная; состояние обновит ответ API.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void loadRun()
  }, [loadRun])

  const isActive = run?.status === "pending" || run?.status === "running"
  useEffect(() => {
    if (!isActive) return
    const timer = setInterval(() => {
      if (inFlight.current) return
      void loadRun()
    }, 5000)
    return () => clearInterval(timer)
  }, [isActive, loadRun])

  useEffect(() => {
    const next = run?.status
    const previous = previousStatus.current
    previousStatus.current = next
    if (!previous || previous === next || !next) return
    statusCallback.current?.(previous, next)
  }, [run?.status])

  return { run, loading, error, isActive, reload: loadRun }
}
