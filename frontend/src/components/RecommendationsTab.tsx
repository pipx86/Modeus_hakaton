import { useEffect, useState } from "react"
import { BookOpenIcon, CheckCircle2Icon, ExternalLinkIcon, ListChecksIcon, Loader2Icon, RefreshCwIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { getRunRecommendations, type Recommendation } from "@/lib/api/client"

const LEVEL_LABELS: Record<string, string> = {
  junior: "Junior",
  middle: "Middle",
  senior: "Senior",
  unknown: "Уровень не определён",
}

export function RecommendationsTab({
  runId,
  status,
  active,
}: {
  runId: string
  status?: string
  active: boolean
}) {
  const [recommendation, setRecommendation] = useState<Recommendation | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const ready = status === "done" || status === "degraded"

  useEffect(() => {
    if (!active || !ready) {
      return
    }
    let cancelled = false
    const load = async () => {
      try {
        const data = await getRunRecommendations(runId)
        if (!cancelled) {
          setRecommendation(data)
          setError(null)
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Не удалось получить рекомендации")
        }
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [active, ready, runId])

  if (!ready) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Рекомендации</CardTitle>
          <CardDescription>
            Курсы и план развития появятся после завершения интервью и анализа.
          </CardDescription>
        </CardHeader>
      </Card>
    )
  }

  if (loading) {
    return (
      <Card>
        <CardContent className="flex items-center gap-2 py-8 text-sm text-muted-foreground">
          <Loader2Icon className="size-4 animate-spin" />
          Подбираем курсы и план развития…
        </CardContent>
      </Card>
    )
  }

  if (error) {
    return (
      <Card>
        <CardContent className="flex flex-col items-start gap-3 py-6">
          <p className="text-sm text-destructive">{error}</p>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              setError(null)
              setLoading(true)
              getRunRecommendations(runId)
                .then(setRecommendation)
                .catch((err: unknown) =>
                  setError(err instanceof Error ? err.message : "Не удалось получить рекомендации"),
                )
                .finally(() => setLoading(false))
            }}
          >
            <RefreshCwIcon className="size-4" />
            Повторить
          </Button>
        </CardContent>
      </Card>
    )
  }

  if (!recommendation) {
    return null
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <div className="flex flex-wrap items-center gap-2">
            <CardTitle className="text-base">План развития</CardTitle>
            <Badge variant="secondary">{LEVEL_LABELS[recommendation.level] ?? recommendation.level}</Badge>
          </div>
          <CardDescription>{recommendation.summary}</CardDescription>
        </CardHeader>
        <CardContent>
          <ol className="space-y-3">
            {recommendation.plan.map((step) => (
              <li key={`${step.order}-${step.title}`} className="flex gap-3">
                <span className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full bg-primary/10 text-xs font-semibold text-primary">
                  {step.order}
                </span>
                <div>
                  <p className="font-medium">{step.title}</p>
                  <p className="text-sm text-muted-foreground">{step.description}</p>
                </div>
              </li>
            ))}
          </ol>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <BookOpenIcon className="size-4" />
            Бесплатные курсы Stepik
          </CardTitle>
          <CardDescription>Подобраны под роль, уровень и пробелы в ответах.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {recommendation.courses.map((course) => (
            <a
              key={course.url}
              href={course.url}
              target="_blank"
              rel="noreferrer"
              className="block rounded-lg border p-3 transition-colors hover:bg-muted/50"
            >
              <div className="flex items-start justify-between gap-3">
                <div>
                  <p className="font-medium">{course.title}</p>
                  <p className="mt-1 text-sm text-muted-foreground">{course.reason}</p>
                  <p className="mt-1 text-xs text-muted-foreground">
                    {course.provider} · {course.free ? "бесплатно" : "платно"}
                  </p>
                </div>
                <ExternalLinkIcon className="mt-1 size-4 shrink-0 text-muted-foreground" />
              </div>
            </a>
          ))}
        </CardContent>
      </Card>

      <p className="flex items-center gap-2 text-xs text-muted-foreground">
        <ListChecksIcon className="size-3.5" />
        План можно использовать как чек-лист для самостоятельного развития.
        <CheckCircle2Icon className="size-3.5" />
      </p>
    </div>
  )
}
