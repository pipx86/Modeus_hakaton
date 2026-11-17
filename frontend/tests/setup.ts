import { afterEach } from "vitest"
import { act } from "@testing-library/react"

/**
 * Дополнительные полифилы для jsdom.
 *
 * Radix-компоненты (Select, Tooltip, Sheet) при открытии вызывают методы Pointer Events,
 * которых в jsdom нет. Без заглушек тесты падают с `hasPointerCapture is not a function`
 * ещё до первого assertion.
 */
import '@testing-library/jest-dom/vitest'
if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false
}

if (!Element.prototype.setPointerCapture) {
  Element.prototype.setPointerCapture = () => undefined
}

if (!Element.prototype.releasePointerCapture) {
  Element.prototype.releasePointerCapture = () => undefined
}

if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => undefined
}

// После каждого теста даём отработать уже запущенным промисам (загрузка данных в useEffect),
// чтобы их setState не приходился на момент, когда тест уже закончился.
afterEach(async () => {
  await act(async () => {})
})
