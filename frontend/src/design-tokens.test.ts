import { existsSync, readFileSync } from "node:fs"
import { expect, test } from "vitest"

const css = readFileSync("src/index.css", "utf8")
const configPath = existsSync("tailwind.config.js")
  ? "tailwind.config.js"
  : "tailwind.config.ts"
const tailwindConfig = readFileSync(configPath, "utf8")

test("index.css объявляет дизайн-токены для светлой и тёмной тем", () => {
  expect(css).toContain(":root")
  expect(css).toContain(".dark")
  expect(css).toContain("--background:")
  expect(css).toContain("--foreground:")
  expect(css).toContain("--primary:")
  expect(css).toContain("--radius:")
})

test("tailwind-конфиг ссылается на токены, а не на хардкод цветов", () => {
  expect(tailwindConfig).toContain("hsl(var(--background))")
  expect(tailwindConfig).toContain("hsl(var(--primary))")
  expect(tailwindConfig).toContain("hsl(var(--muted))")
})