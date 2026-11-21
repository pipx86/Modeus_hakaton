import { useEffect, useState } from "react"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { listSourceChanges, type SourceChange } from "@/lib/api/client"

export function SourceChanges({ runId, onFocusNode }: { runId: string; onFocusNode: (id: string) => void }) {
  const [items, setItems] = useState<SourceChange[]>([])
  const [error, setError] = useState(false)
  const [loading, setLoading] = useState(true)
  const [reloadToken, setReloadToken] = useState(0)

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      setLoading(true)
      try {
        const data = await listSourceChanges(runId)
        if (cancelled) return
        setItems(data)
        setError(false)
      } catch {
        if (cancelled) return
        setError(true)
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    // Загрузка асинхронная: состояние обновится после ответа API.
    void load()
    return () => {
      cancelled = true
    }
  }, [runId, reloadToken])

  return (
    <Card>
      <CardHeader>
        <CardTitle>Изменения использованных источников</CardTitle>
        <CardDescription>Источники изменились после создания обоснований. Проверьте, влияют ли изменения на узлы дерева. Учитываются изменения после включения этой функции.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-2">
        {loading ? <p>Загрузка…</p> : error ? (
          <div role="alert">Не удалось получить изменения. <Button variant="outline" size="sm" onClick={() => setReloadToken((token) => token + 1)}>Повторить загрузку изменений</Button></div>
        ) : items.length === 0 ? <p className="text-sm text-muted-foreground">Новых изменений использованных источников не обнаружено.</p> : (
          <ul className="space-y-2">
            {items.map((item) => (
              <li key={item.evidence_id} className="rounded-lg border p-3 text-sm">
                <p>{item.source_title} · {new Date(item.changed_at).toLocaleDateString("ru-RU")}</p>
                <Button variant="link" className="h-auto px-0" onClick={() => onFocusNode(item.node_id)}>{item.node_title}</Button>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}
