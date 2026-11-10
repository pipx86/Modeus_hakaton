import { Badge } from "@/components/ui/badge"
import { cn } from "cn"
import type { RunStatus } from "@/lib/api/client"

const STATUS_CONFIG: Record<RunStatus, { label: string; className: string }> = {
  pending: {
    label: "Ожидание",
    className: "border-transparent bg-muted text-muted-foreground",
  },
  running: {
    label: "В работе",
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
}

export function StatusBadge({ status }: { status?: RunStatus }) {
  const config = STATUS_CONFIG[status ?? "pending"]

  return (
    <Badge variant="outline" className={cn("gap-1.5 rounded-full font-medium", config.className)}>
      <span className="size-1.5 rounded-full bg-current" />
      {config.label}
    </Badge>
  )
}
