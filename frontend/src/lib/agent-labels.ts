const AGENT_LABELS: Record<string, string> = {
  interviewer: "Интервьюер",
  analyst: "Аналитик",
  synthesizer: "Синтезатор",
  learning: "Планировщик обучения",
  "learning-planner": "Планировщик обучения",
  learning_planner: "Планировщик обучения",
  planner: "Планировщик обучения",
  interviewer_agent: "Интервьюер",
  analyst_agent: "Аналитик",
  synthesizer_agent: "Синтезатор",
  unknown: "Неизвестный агент",
}

/** Русское название агента для интерфейса; неизвестные значения не теряются. */
export function agentLabel(value: string): string {
  const normalized = value.trim().toLowerCase()
  return AGENT_LABELS[normalized] ?? value
}
