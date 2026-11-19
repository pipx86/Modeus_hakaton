import { useEffect, useState } from "react"
import { BookOpenIcon, CheckCircle2Icon, Loader2Icon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { getLearningPlan, type LearningPlan } from "@/lib/api/client"

/**
 * Показывает сохранённый план обучения исследования. План генерируется
 * фоновым агентом, поэтому компонент сначала ждёт завершения прогона.
 */
export function LearningPlanView({
  runId,
  status,
}: {
  runId: string
  status?: string
}) {
  const [plan, setPlan] = useState<LearningPlan | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [reloadToken, setReloadToken] = useState(0)

  useEffect(() => {
    if (status !== "done") return
    let cancelled = false
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true)
    setError(null)
    void (async () => {
      try {
        const result = await getLearningPlan(runId)
        if (!cancelled) setPlan(result)
      } catch (cause) {
        if (!cancelled) {
          setError(cause instanceof Error ? cause.message : "Не удалось загрузить план обучения.")
        }
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [runId, status, reloadToken])

  if (status === "failed") {
    return (
      <Card className="p-5">
        <CardHeader className="px-0">
          <CardTitle>План не составлен</CardTitle>
          <CardDescription>
            Агент не смог построить план обучения. Запустите подбор ещё раз из раздела «Новое
            исследование».
          </CardDescription>
        </CardHeader>
      </Card>
    )
  }

  if (status !== "done" || loading) {
    return (
      <Card className="p-5">
        <CardHeader className="px-0">
          <CardTitle className="flex items-center gap-2">
            <Loader2Icon className="size-5 animate-spin text-primary" />
            Агенты составляют план обучения
          </CardTitle>
          <CardDescription>
            Собираем требования рынка, анализируем профессию и формируем пошаговый план. Это
            займёт около минуты.
          </CardDescription>
        </CardHeader>
      </Card>
    )
  }

  if (error || !plan) {
    return (
      <Card className="p-5">
        <CardHeader className="px-0">
          <CardTitle>План пока недоступен</CardTitle>
          <CardDescription>{error ?? "Попробуйте загрузить план ещё раз."}</CardDescription>
        </CardHeader>
        <CardContent className="px-0">
          <Button variant="outline" onClick={() => setReloadToken((value) => value + 1)}>
            Повторить загрузку
          </Button>
        </CardContent>
      </Card>
    )
  }

  return (
    <Card className="gap-4 p-5">
      <CardHeader className="px-0">
        <CardTitle className="flex items-center gap-2">
          <CheckCircle2Icon className="size-5 text-success" />
          План обучения: {plan.profession}
        </CardTitle>
        <CardDescription>
          <BookOpenIcon className="mr-1 inline size-3.5" />
          {plan.summary || "Пошаговый план входа в профессию."}
          {plan.weeks > 0 && ` Ориентир: ${plan.weeks} нед.`}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4 px-0">
        {plan.stages.map((stage, index) => (
          <div key={`${stage.title}-${index}`} className="rounded-xl border p-4">
            <div className="flex items-start gap-3">
              <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-primary/10 text-sm font-semibold text-primary">
                {index + 1}
              </span>
              <div className="min-w-0 flex-1 space-y-3">
                <div>
                  <h3 className="font-medium">{stage.title}</h3>
                  {stage.goal && (
                    <p className="mt-0.5 text-sm text-muted-foreground">{stage.goal}</p>
                  )}
                </div>
                {stage.topics.length > 0 && (
                  <div>
                    <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                      Изучить
                    </p>
                    <ul className="mt-1 grid gap-1 sm:grid-cols-2">
                      {stage.topics.map((topic) => (
                        <li key={topic} className="flex gap-2 text-sm">
                          <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-primary" />
                          {topic}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
                {stage.practice.length > 0 && (
                  <div>
                    <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                      Практика
                    </p>
                    <ul className="mt-1 space-y-1">
                      {stage.practice.map((item) => (
                        <li key={item} className="flex gap-2 text-sm">
                          <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-success" />
                          {item}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
              </div>
            </div>
          </div>
        ))}
        {plan.sources && plan.sources.length > 0 && (
          <p className="text-xs text-muted-foreground">Источники: {plan.sources.join("; ")}</p>
        )}
      </CardContent>
    </Card>
  )
}
