import { defineConfig, loadEnv, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import fs from 'node:fs'
import path from 'path'
import { fileURLToPath } from 'url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

/** Пишет присланные фронтендом записи в frontend/logs/frontend.log (только dev). */
function frontendLogCollector(): Plugin {
  return {
    name: "frontend-log-collector",
    apply: "serve",
    configureServer(server) {
      server.middlewares.use("/__frontend-log", (req, res) => {
        if (req.method !== "POST") {
          res.statusCode = 405
          res.end()
          return
        }
        let body = ""
        req.setEncoding("utf8")
        req.on("data", (chunk: string) => {
          body += chunk
        })
        req.on("end", () => {
          try {
            const dir = path.resolve(__dirname, "logs")
            fs.mkdirSync(dir, { recursive: true })
            const file = path.join(dir, "frontend.log")
            try {
              if (fs.statSync(file).size > 5 * 1024 * 1024) {
                fs.renameSync(file, path.join(dir, "frontend.log.prev"))
              }
            } catch {
              // файла ещё нет — просто создадим
            }
            fs.appendFileSync(file, `${body.trim()}\n`, "utf8")
          } catch {
            // Сбор логов не должен ломать dev-сервер.
          }
          res.statusCode = 204
          res.end()
        })
      })
    },
  }
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')

  return {
    plugins: [react(), frontendLogCollector()],
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "./src"),
      },
    },
    css: {
      transformer: 'postcss',
    },
    build: {
      minify: false,
    },
    server: {
      proxy: {
        // Живой бэкенд: прокси добавляет JWT, потому что EventSource не умеет заголовки.
        "/api": {
          target: env.VITE_PROXY_TARGET || "http://127.0.0.1:8080",
          changeOrigin: true,
          selfHandleResponse: true,
          configure: (proxy) => {
            proxy.on("proxyReq", (proxyReq, req) => {
              if (env.MODEUS_TOKEN) {
                proxyReq.setHeader("Authorization", `Bearer ${env.MODEUS_TOKEN}`)
              }
              if (String(req.url ?? "").includes("/events")) {
                proxyReq.setHeader("Accept-Encoding", "identity")
              }
            })
            // Отдаём статус и заголовки бэкенда сразу, тело — потоком (важно для SSE).
            proxy.on("proxyRes", (proxyRes, _req, res) => {
              res.writeHead(proxyRes.statusCode ?? 502, proxyRes.headers)
              proxyRes.pipe(res)
            })
          },
        },
      },
    },
  }
})
