import type { NodeType, RunStatus, SourceType } from "@/lib/api/client"

export const RUN_STATUS_LABEL: Record<RunStatus, string> = {
  pending: "В очереди",
  running: "Выполняется",
  done: "Завершён",
  failed: "Ошибка",
}

export const RUN_STATUS_VARIANT: Record<RunStatus, "outline" | "default" | "secondary" | "destructive"> = {
  pending: "outline",
  running: "default",
  done: "secondary",
  failed: "destructive",
}

export const NODE_TYPE_LABEL: Record<NodeType, string> = {
  role: "Роль",
  function: "Функция",
  task: "Задача",
  action: "Действие",
}

export const SOURCE_TYPE_LABEL: Record<SourceType, string> = {
  vacancy: "Вакансия",
  interview: "Интервью",
  standard: "Профстандарт",
  document: "Документ",
}