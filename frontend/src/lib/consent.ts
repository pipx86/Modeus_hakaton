/**
 * @file consent.ts
 * @description Событие о принятии согласия: страница `/consent` сообщает
 * баннеру в Layout, что напоминание больше не нужно.
 */

const CONSENT_ACCEPTED_EVENT = "modeus:consent-accepted"

/** Сообщает интерфейсу, что согласие принято. */
export function notifyConsentAccepted() {
  window.dispatchEvent(new Event(CONSENT_ACCEPTED_EVENT))
}

/** Подписка на принятие согласия; возвращает функцию отписки. */
export function subscribeConsentAccepted(listener: () => void) {
  window.addEventListener(CONSENT_ACCEPTED_EVENT, listener)
  return () => window.removeEventListener(CONSENT_ACCEPTED_EVENT, listener)
}
