/**
 * @file security.ts
 * @description Защитные утилиты фронтенда: нормализация пользовательского текста
 * и проверка внешних ссылок.
 *
 * SQL-инъекции здесь ни при чём: фронтенд не формирует SQL, запросы уходят в API,
 * где используются параметризованные запросы pgx/sqlc. XSS закрывается тем, что React
 * экранирует текст, а опасные приёмы (`dangerouslySetInnerHTML`, `innerHTML`) запрещены
 * правилом ESLint. Эти функции - дополнительный слой для мест, где появится разметка
 * или внешние ссылки.
 */

/** Управляющие символы, которые не должны попадать в тексты, заголовки и логи. */
// eslint-disable-next-line no-control-regex -- управляющие символы здесь являются целью фильтра
const CONTROL_CHARS = /[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F]/g

/**
 * Удаляет управляющие символы (кроме `\t`, `\n`, `\r`) из пользовательского текста.
 * Не заменяет экранирование: React по-прежнему рендерит результат как текст.
 */
export function stripControlChars(value: string): string {
  return value.replace(CONTROL_CHARS, "")
}

/**
 * Возвращает нормализованный URL, только если это `http:` или `https:`.
 * Всё остальное (`javascript:`, `data:`, `vbscript:`, относительные пути, мусор)
 * превращается в `null`, чтобы такой адрес нельзя было подставить в `href`.
 */
export function safeHttpUrl(value?: string | null): string | null {
  if (!value) return null
  const trimmed = value.trim()
  if (!trimmed) return null
  try {
    const url = new URL(trimmed)
    if (url.protocol === "http:" || url.protocol === "https:") return url.toString()
  } catch {
    return null
  }
  return null
}
