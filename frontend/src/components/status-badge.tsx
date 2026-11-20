/**
 * @file status-badge.tsx
 * @description Бейдж статуса прогона: цвет, подпись и доступное имя.
 */

import { Badge } from "@/components/ui/badge"
import { cn } from "cn"
import type { RunStatus } from "@/lib/api/client"

const STATUS_CONFIG: Record<RunStatus, { label: string; className: string }> = {
  pending: {
    label: "Ожидание",
    className: "border-transparent bg-muted text-muted-foreground",
  },
  running: {
    label: "Интервью",
    className: "border-transparent bg-warning/15 text-warning",
  },
  done: {
    label: "Завершено",
    className: "border-transparent bg-success/15 text-success",
  },
  failed: {
    label: "Ошибка",
    className: "border-transparent bg-destructive/15 text-destructive",
  },
  degraded: {
    label: "С ограничениями",
    className: "border-transparent bg-warning/15 text-warning",
  },
}

export function StatusBadge({ status }: { status?: RunStatus }) {
  const config = STATUS_CONFIG[status ?? "pending"]
  return (
    <Badge
      variant="outline"
      className={cn("h-6 rounded-full px-2.5 font-medium", config.className)}
    >
      <span className="-translate-y-[1px]">{config.label}</span>
    </Badge>
  )
}
