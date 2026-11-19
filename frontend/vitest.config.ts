import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import path from 'path'
import { fileURLToPath } from 'url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

/**
 * Готовый `vitest.config.ts` для проекта с отдельной папкой tests.
 * Скопируйте содержимое в frontend/vitest.config.ts (заменив текущее).
 */
export default defineConfig({
  plugins: [react()],
  resolve: {
    dedupe: ['react', 'react-dom', '@testing-library/react', 'vitest'],
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    fs: { allow: [__dirname, path.resolve(__dirname, "../tests/frontend")] },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts', './tests/setup.ts'],
    include: ['tests/**/*.test.{ts,tsx}', '../tests/frontend/**/*.test.{ts,tsx}'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html', 'lcov'],
      reportsDirectory: './coverage',
      include: ['src/**/*.{ts,tsx}'],
      exclude: [
        'src/**/*.test.{ts,tsx}',
        'src/test/**',
        'src/components/ui/**',
        'src/**/*.d.ts',
        'src/main.tsx',
      ],
    },
  },
})
