import { useState } from "react"
import { Button } from "@/components/ui/button"
import { setReviewAccess } from "@/lib/api/client"

export function ReviewAccess({ runId }: { runId: string }) {
  const [email, setEmail] = useState("")
  const [message, setMessage] = useState("")
  const [busy, setBusy] = useState(false)
  const update = async (grant: boolean) => {
    setBusy(true)
    try {
      await setReviewAccess(runId, email.trim(), grant)
      setMessage(grant ? "Доступ предоставлен специалисту." : "Доступ отозван.")
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Не удалось изменить доступ.")
    } finally { setBusy(false) }
  }
  return <section className="space-y-2 rounded-lg border p-4">
    <h2 className="font-medium">Доступ методиста</h2>
    <p className="text-sm text-muted-foreground">Укажите email зарегистрированного специалиста, которому можно просматривать материалы этого исследования.</p>
    <label className="block text-sm">Email специалиста<input type="email" className="ml-2 rounded border p-2" value={email} onChange={(event) => setEmail(event.target.value)} /></label>
    <div className="flex gap-2"><Button size="sm" disabled={busy || !email.trim()} onClick={() => void update(true)}>Предоставить доступ</Button><Button variant="outline" size="sm" disabled={busy || !email.trim()} onClick={() => void update(false)}>Отозвать доступ</Button></div>
    {message && <p role="status" className="text-sm">{message}</p>}
  </section>
}
