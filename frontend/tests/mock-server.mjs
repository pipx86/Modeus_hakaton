import { createServer } from "node:http"

const PORT = Number(process.env.MOCK_PORT ?? 8080)
const PREFIX = "/api/v1"

const CORS = {
  "Access-Control-Allow-Origin": "*",
  "Access-Control-Allow-Methods": "GET,POST,PATCH,DELETE,OPTIONS",
  "Access-Control-Allow-Headers": "Content-Type",
}

let counter = 0
const nextId = (prefix) => `${prefix}-${++counter}`
const now = () => new Date().toISOString()

const send = (res, status, body) => {
  res.writeHead(status, { ...CORS, "Content-Type": "application/json; charset=utf-8" })
  res.end(body === undefined ? "" : JSON.stringify(body))
}
const noContent = (res) => {
  res.writeHead(204, CORS)
  res.end()
}
const notFound = (res, message = "Маршрут не найден в мок-сервере") =>
  send(res, 404, { code: "NOT_FOUND", message })
const validation = (res, message) => send(res, 422, { code: "VALIDATION_FAILED", message })

const readJson = (req) =>
  new Promise((resolve, reject) => {
    let raw = ""
    req.on("data", (chunk) => {
      raw += chunk
    })
    req.on("end", () => {
      if (!raw) return resolve({})
      try {
        resolve(JSON.parse(raw))
      } catch {
        reject(new Error("bad json"))
      }
    })
    req.on("error", reject)
  })

// ---------------- хранилище ----------------
const db = {
  runs: new Map(),
  trees: new Map(), // runId -> Node[] (плоский массив, иерархия через parent_id)
  messages: new Map(), // runId -> ChatMessage[]
  sources: new Map(),
  evidences: new Map(),
  approved: new Set(),
  interview: new Map(), // runId -> { step, autoTimer }
}
const sse = new Map() // runId -> Set<response>

function emit(runId, event, data) {
  const clients = sse.get(runId)
  if (!clients) return
  const frame = `event: ${event}\ndata: ${JSON.stringify(data ?? {})}\n\n`
  for (const client of clients) client.write(frame)
}

// ---------------- сид-данные для демо ----------------
const seedSources = [
  {
    id: "src-1",
    type: "vacancy",
    title: "Вакансия Data Scientist, HH",
    content: "Опыт с pandas, SQL и A/B-тестами обязателен.",
    created_at: now(),
  },
  {
    id: "src-2",
    type: "standard",
    title: "Профстандарт «Специалист по данным»",
    content: "Трудовые функции: сбор, обработка и первичный анализ данных.",
    created_at: now(),
  },
]
for (const source of seedSources) db.sources.set(source.id, source)

const seedEvidence = [
  { id: "ev-1", source_id: "src-1", quote: "Опыт с pandas, SQL и A/B-тестами обязателен" },
  { id: "ev-2", source_id: "src-2", quote: "Трудовая функция: обработка и первичный анализ данных" },
]
const seedStatus = ["pending", "approved"]
seedEvidence.forEach((evidence, index) => {
  evidence.status = seedStatus[index] ?? "pending"
  evidence.confidence = index === 0 ? 0.82 : 0.94
  db.evidences.set(evidence.id, evidence)
})

db.trees.set("run-demo", [
  { id: "n-1", type: "role", title: "Data Scientist", description: "Специалист по данным продуктовой команды", evidence: [] },
  { id: "n-2", type: "function", title: "Анализ продуктовых метрик", parent_id: "n-1", evidence: [seedEvidence[0]] },
  { id: "n-3", type: "function", title: "Подготовка данных", parent_id: "n-1", evidence: [seedEvidence[1]] },
  { id: "n-4", type: "task", title: "Построение когортных отчётов", parent_id: "n-2", evidence: [seedEvidence[0]] },
  { id: "n-5", type: "task", title: "Чистка и нормализация датасетов", parent_id: "n-3", evidence: [seedEvidence[1]] },
  { id: "n-6", type: "action", title: "Сводит когортный отчёт в SQL", parent_id: "n-4", evidence: [seedEvidence[0]],
    zun: {
      knowledge: ["Устройство когортного анализа", "Диалект SQL и оконные функции"],
      skills: ["Написание аналитических запросов", "Проверка отчёта на выбросы"],
      abilities: ["Точно формулировать метрики"],
    } },
  { id: "n-7", type: "action", title: "Удаляет дубликаты и выбросы", parent_id: "n-5", evidence: [seedEvidence[1]],
    zun: {
      knowledge: ["Методы очистки данных", "Критерии выбросов"],
      skills: ["Работа с pandas", "Профилирование датасета"],
      abilities: ["Внимательность к деталям"],
    } },
])

db.runs.set("run-demo", {
  id: "run-demo",
  status: "done",
  profession: "Data Scientist (демо)",
  root_node_id: "n-1",
  started_at: now(),
  finished_at: now(),
})
db.messages.set("run-demo", [
  { id: "msg-1", role: "agent", content: "Расскажите, какие задачи вы решаете чаще всего?", created_at: now() },
  { id: "msg-2", role: "user", content: "Анализ метрик и подготовка отчётов для продуктовой команды.", created_at: now() },
  { id: "msg-3", role: "agent", content: "Спасибо! Этого достаточно - дерево деятельности собрано.", created_at: now(), citations: [{ title: "Анализ продуктовых метрик", quote: "Опыт с pandas, SQL и A/B-тестами обязателен" }] },
])

// ---------------- жизненный цикл прогона и интервью ----------------
const INTERVIEW_QUESTIONS = [
  "Опишите типичную рабочую неделю: какие задачи занимают больше всего времени?",
  "Какие инструменты и системы вы используете ежедневно?",
  "Какие решения вы принимаете самостоятельно, а какие согласуете?",
]

function pushMessage(runId, role, content) {
  const message = { id: nextId("msg"), role, content, created_at: now() }
  const list = db.messages.get(runId) ?? []
  list.push(message)
  db.messages.set(runId, list)
  return message
}

function agentSay(runId, content) {
  const message = pushMessage(runId, "agent", content)
  emit(runId, "agent_message", { type: "agent_message", message })
  return message
}

function setRunStatus(runId, status) {
  const run = db.runs.get(runId)
  if (!run) return
  run.status = status
  if (status === "done" || status === "failed") run.finished_at = now()
  emit(runId, "run_status", { type: "run_status", status })
}

function finalizeRun(runId) {
  const run = db.runs.get(runId)
  if (!run || run.status === "done" || run.status === "failed") return
  const sourceIds = [...db.sources.keys()]
  const makeEvidence = (index) => {
    const sourceId = sourceIds[index % Math.max(sourceIds.length, 1)] ?? "src-1"
    const source = db.sources.get(sourceId)
    const evidence = {
      id: nextId("ev"),
      source_id: sourceId,
      status: "pending",
      confidence: 0.75,
      quote: source ? `Фрагмент «${source.title}»: типичная задача специалиста` : "Уточнение из интервью",
    }
    db.evidences.set(evidence.id, evidence)
    return evidence
  }
  const role = { id: nextId("node"), type: "role", title: run.profession, evidence: [] }
  const f1 = { id: nextId("node"), type: "function", title: "Основная деятельность", parent_id: role.id, evidence: [makeEvidence(0)] }
  const f2 = { id: nextId("node"), type: "function", title: "Коммуникация и согласования", parent_id: role.id, evidence: [makeEvidence(1)] }
  const t1 = { id: nextId("node"), type: "task", title: "Ежедневные операции", parent_id: f1.id, evidence: [makeEvidence(0)] }
  const a1 = { id: nextId("node"), type: "action", title: "Выполняет типовую операцию", parent_id: t1.id, evidence: [makeEvidence(1)] }
  db.trees.set(runId, [role, f1, f2, t1, a1])
  run.root_node_id = role.id
  agentSay(runId, "Интервью завершено. Дерево деятельности собрано - спасибо!")
  setRunStatus(runId, "done")
}

function askNext(runId) {
  const run = db.runs.get(runId)
  const state = db.interview.get(runId)
  if (!run || !state || run.status !== "running") return
  emit(runId, "agent_thinking", { type: "agent_thinking" })
  setTimeout(() => {
    if (state.step < INTERVIEW_QUESTIONS.length) {
      agentSay(runId, INTERVIEW_QUESTIONS[state.step])
      state.step += 1
      state.autoTimer = setTimeout(() => finalizeRun(runId), 45000)
    } else {
      setTimeout(() => finalizeRun(runId), 1200)
    }
  }, 1500)
}

function replyReceived(runId) {
  const state = db.interview.get(runId)
  if (!state) return
  if (state.autoTimer) clearTimeout(state.autoTimer)
  setTimeout(() => askNext(runId), 300)
}

function startRun(runId) {
  db.interview.set(runId, { step: 0, autoTimer: undefined })
  setTimeout(() => {
    setRunStatus(runId, "running")
    setTimeout(() => askNext(runId), 1000)
  }, 1000)
}

// ---------------- SSE ----------------
function openSse(runId, req, res) {
  if (!db.runs.has(runId)) return notFound(res, "run не найден")
  res.writeHead(200, {
    ...CORS,
    "Content-Type": "text/event-stream; charset=utf-8",
    "Cache-Control": "no-cache",
    Connection: "keep-alive",
  })
  res.write("event: ping\ndata: {}\n\n")
  const clients = sse.get(runId) ?? new Set()
  clients.add(res)
  sse.set(runId, clients)
  const ping = setInterval(() => res.write("event: ping\ndata: {}\n\n"), 15000)
  req.on("close", () => {
    clearInterval(ping)
    clients.delete(res)
  })
}

// ---------------- роутер ----------------
const server = createServer(async (req, res) => {
  const url = new URL(req.url ?? "/", `http://${req.headers.host ?? "localhost"}`)
  const path = url.pathname
  if (!path.startsWith(PREFIX)) return notFound(res)
  const route = path.slice(PREFIX.length) || "/"
  const method = req.method ?? "GET"

  if (method === "OPTIONS") return noContent(res)

  try {
    if (method === "GET" && route === "/health") return send(res, 200, { status: "ok" })

    if (method === "GET" && route === "/runs") {
      const runs = [...db.runs.values()].sort((a, b) =>
        (b.started_at ?? "").localeCompare(a.started_at ?? ""),
      )
      return send(res, 200, runs)
    }
    if (method === "POST" && route === "/runs") {
      const body = await readJson(req)
      if (body.type === "learning") return validation(res, "Создание новых планов обучения отключено")
      if (typeof body.profession !== "string" || body.profession.trim().length === 0) {
        return validation(res, "profession: обязательное поле")
      }
      const run = {
        id: nextId("run"),
        type: body.type ?? "chat",
        status: "pending",
        profession: body.profession.trim(),
        started_at: now(),
      }
      db.runs.set(run.id, run)
      db.trees.set(run.id, [])
      db.messages.set(run.id, [])
      startRun(run.id)
      return send(res, 201, run)
    }

    let match = route.match(/^\/runs\/([^/]+)$/)
    if (match && method === "GET") {
      const run = db.runs.get(match[1])
      return run ? send(res, 200, run) : notFound(res, "run не найден")
    }

    match = route.match(/^\/runs\/([^/]+)\/tree$/)
    if (match && method === "GET") {
      if (!db.runs.has(match[1])) return notFound(res, "run не найден")
      return send(res, 200, db.trees.get(match[1]) ?? [])
    }

    match = route.match(/^\/runs\/([^/]+)\/messages$/)
    if (match && method === "GET") {
      if (!db.runs.has(match[1])) return notFound(res, "run не найден")
      return send(res, 200, db.messages.get(match[1]) ?? [])
    }
    if (match && method === "POST") {
      const run = db.runs.get(match[1])
      if (!run) return notFound(res, "run не найден")
      if (run.status === "done" || run.status === "failed") {
        return send(res, 409, { code: "RUN_FINISHED", message: "Прогон завершён и не принимает ответы" })
      }
      const body = await readJson(req)
      if (typeof body.content !== "string" || body.content.trim().length === 0) {
        return validation(res, "content: обязательное поле")
      }
      const message = pushMessage(run.id, "user", body.content.trim())
      replyReceived(run.id)
      return send(res, 201, message)
    }

    match = route.match(/^\/runs\/([^/]+)\/events$/)
    if (match && method === "GET") return openSse(match[1], req, res)

    if (method === "GET" && route === "/sources") return send(res, 200, [...db.sources.values()])
    if (method === "POST" && route === "/sources") {
      const body = await readJson(req)
      const types = ["vacancy", "interview", "standard", "document"]
      if (!types.includes(body.type)) {
        return validation(res, "type: допустимы vacancy, interview, standard, document")
      }
      if (typeof body.title !== "string" || body.title.trim().length === 0) {
        return validation(res, "title: обязательное поле")
      }
      const source = { id: nextId("src"), type: body.type, title: body.title.trim(), created_at: now() }
      if (typeof body.content === "string" && body.content.length > 0) source.content = body.content
      db.sources.set(source.id, source)
      return send(res, 201, source)
    }
    match = route.match(/^\/sources\/([^/]+)$/)
    if (match && method === "GET") {
      const source = db.sources.get(match[1])
      return source ? send(res, 200, source) : notFound(res, "источник не найден")
    }
    if (match && method === "DELETE") {
      if (!db.sources.delete(match[1])) return notFound(res, "источник не найден")
      return noContent(res)
    }

    if (method === "GET" && route === "/evidences") {
      let list = [...db.evidences.values()]
      const sourceId = url.searchParams.get("source_id")
      const nodeId = url.searchParams.get("node_id")
      if (sourceId) list = list.filter((evidence) => evidence.source_id === sourceId)
      if (nodeId) {
        const inNode = new Set()
        for (const nodes of db.trees.values()) {
          for (const node of nodes) for (const evidence of node.evidence ?? []) inNode.add(evidence.id)
        }
        list = list.filter((evidence) => inNode.has(evidence.id))
      }
      return send(res, 200, list)
    }
    if (method === "POST" && route === "/evidences") {
      const body = await readJson(req)
      if (!db.sources.has(body.source_id)) return validation(res, "source_id: источник не найден")
      if (typeof body.quote !== "string" || body.quote.trim().length === 0) {
        return validation(res, "quote: обязательное поле")
      }
      const evidence = {
        id: nextId("ev"),
        source_id: body.source_id,
        quote: body.quote.trim(),
        status: "pending",
        confidence: 0.8,
      }
      db.evidences.set(evidence.id, evidence)
      return send(res, 201, evidence)
    }
    match = route.match(/^\/evidences\/([^/]+)\/approve$/)
    if (match && method === "POST") {
      if (!db.evidences.has(match[1])) return notFound(res, "цитата не найдена")
      const approved = db.evidences.get(match[1])
      if (approved) {
        approved.status = "approved"
        approved.confidence ??= 0.9
      }
      db.approved.add(match[1])
      return noContent(res)
    }

    match = route.match(/^\/evidences\/([^/]+)\/reject$/)
    if (match && method === "POST") {
      const evidence = db.evidences.get(match[1])
      if (!evidence) return notFound(res, "цитата не найдена")
      if (evidence.status === "rejected") {
        return send(res, 409, { code: "ALREADY_REJECTED", message: "цитата уже отклонена" })
      }
      evidence.status = "rejected"
      db.approved.delete(match[1])
      return noContent(res)
    }

    match = route.match(/^\/evidences\/([^/]+)$/)
    if (match && method === "PATCH") {
      const evidence = db.evidences.get(match[1])
      if (!evidence) return notFound(res, "цитата не найдена")
      const body = await readJson(req)
      if (typeof body.quote === "string" && body.quote.trim()) {
        evidence.quote = body.quote.trim()
        evidence.status = "pending"
        db.approved.delete(match[1])
      }
      if (typeof body.confidence === "number") evidence.confidence = body.confidence
      if (typeof body.ref_id === "string") evidence.ref_id = body.ref_id
      return send(res, 200, evidence)
    }

    match = route.match(/^\/nodes\/([^/]+)$/)
    if (match && method === "PATCH") {
      const body = await readJson(req)
      for (const nodes of db.trees.values()) {
        const node = nodes.find((item) => item.id === match[1])
        if (!node) continue
        if (typeof body.title === "string" && body.title.trim().length > 0) node.title = body.title.trim()
        if (typeof body.description === "string") node.description = body.description
        if (typeof body.type === "string") node.type = body.type
        if ("parent_id" in body) node.parent_id = body.parent_id || undefined
        if (Array.isArray(body.evidence) && body.evidence.length > 0) {
          node.evidence = body.evidence
          for (const evidence of body.evidence) db.evidences.set(evidence.id, evidence)
        }
        return send(res, 200, node)
      }
      return notFound(res, "узел не найден")
    }

    return notFound(res)
  } catch {
    return send(res, 400, { code: "BAD_REQUEST", message: "Некорректный JSON в теле запроса" })
  }
})

server.listen(PORT, () => {
  console.log(`[mock] Modeus activity-descriptor API (cards 1-2): http://localhost:${PORT}${PREFIX}`)
  console.log("[mock] Сид: run-demo (done) с деревом и цитатами. Новый прогон через UI оживёт сам.")
})