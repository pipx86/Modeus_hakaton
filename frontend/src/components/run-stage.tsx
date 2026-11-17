import { CheckIcon } from "lucide-react"
import type { Run } from "@/lib/api/client"

/** Этапы прогона в порядке прохождения. */
const RUN_STAGES = [
  { key: "created", label: "Создано" },
  { key: "interview", label: "Интервью" },
  { key: "analysis", label: "Анализ" },
  { key: "synthesis", label: "Синтез" },
  { key: "review", label: "Проверка" },
  { key: "done", label: "Готово" },
] as const

function stageIndex(stage?: Run["stage"]): number {
  if (!stage || stage === "failed") return -1
  return RUN_STAGES.findIndex((step) => step.key === stage)
}

/**
 * Полоса этапов прогона. Показывает, где сейчас находится исследование,
 * чтобы ожидание не выглядело «вечным»: видно, что прогон на интервью или синтезе.
 */
export function RunStageStepper({ stage, failed }: { stage?: Run["stage"]; failed?: boolean }) {
  const current = stageIndex(stage)
  const isDone = stage === "done"

  return (
    <ol
      className="flex flex-wrap items-center gap-x-1 gap-y-1.5 text-xs"
      aria-label="Этапы исследования"
    >
      {RUN_STAGES.map((step, index) => {
        const complete = isDone || (current >= 0 && index < current)
        const active = failed ? false : current >= 0 && index === current
        return (
          <li key={step.key} className="flex items-center gap-1.5">
            {index > 0 && (
              <span
                aria-hidden
                className={`h-px w-4 ${complete || active ? "bg-primary/60" : "bg-border"}`}
              />
            )}
            <span
              className={`flex size-4 items-center justify-center rounded-full border text-[10px] ${
                failed && index === Math.max(0, current)
                  ? "border-destructive bg-destructive/15 text-destructive"
                  : complete
                    ? "border-primary/50 bg-primary/15 text-primary"
                    : active
                      ? "border-primary bg-primary text-primary-foreground"
                      : "border-border text-muted-foreground"
              }`}
            >
              {complete ? <CheckIcon className="size-2.5" /> : index + 1}
            </span>
            <span
              className={
                active
                  ? "font-medium text-foreground"
                  : complete
                    ? "text-muted-foreground"
                    : "text-muted-foreground/70"
              }
            >
              {step.label}
            </span>
          </li>
        )
      })}
      {failed && <li className="ml-1 font-medium text-destructive">Ошибка</li>}
    </ol>
  )
}
