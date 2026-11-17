import { Link } from "react-router-dom"
import { CompassIcon } from "lucide-react"
import { Button } from "@/components/ui/button"

/** Заглушка для неизвестных маршрутов, чтобы вместо пустого экрана был понятный выход. */
export function NotFound() {
  return (
    <div className="flex flex-col items-center justify-center gap-4 rounded-xl border border-dashed py-20 text-center">
      <div className="flex size-12 items-center justify-center rounded-full bg-muted">
        <CompassIcon className="size-6 text-muted-foreground" />
      </div>
      <div className="space-y-1">
        <h1 className="text-lg font-semibold">Страница не найдена</h1>
        <p className="text-sm text-muted-foreground">
          Возможно, ссылка устарела или исследование было удалено.
        </p>
      </div>
      <Button asChild>
        <Link to="/">К списку исследований</Link>
      </Button>
    </div>
  )
}
