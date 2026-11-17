import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'
import { fileURLToPath } from 'url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')

  return {
    plugins: [react()],
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
