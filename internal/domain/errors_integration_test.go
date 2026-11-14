package domain

import (
	"errors"
	"fmt"
	"testing"
)

// TestDomainErrorsIntegration демонстрирует полный цикл работы с расширенным доменом ошибок.
// Этот тест доказывает, что инфраструктура готова к использованию во всем бэкенде.
func TestDomainErrorsIntegration(t *testing.T) {
	t.Run("Стандартные ошибки работают как синглтоны", func(t *testing.T) {
		// Проверяем, что глобальные ошибки имеют правильные коды и сообщения
		if ErrNotFound.Code != CodeNotFound {
			t.Errorf("ErrNotFound.Code = %q, want %q", ErrNotFound.Code, CodeNotFound)
		}
		if ErrValidation.Message == "" {
			t.Error("ErrValidation.Message пуст")
		}

		// Проверяем, что это действительно одна и та же ошибка (синглтон)
		err1 := ErrNotFound
		err2 := ErrNotFound
		if err1 != err2 {
			t.Error("Ожидали, что ErrNotFound будет одним и тем же экземпляром")
		}
	})

	t.Run("Фабрика NewDomainError сохраняет причину (Cause)", func(t *testing.T) {
		rootCause := errors.New("database connection timeout")
		domainErr := NewDomainError(CodeInternal, "не удалось получить данные", rootCause)

		if domainErr.Code != CodeInternal {
			t.Errorf("Code = %q, want %q", domainErr.Code, CodeInternal)
		}
		if domainErr.Message != "не удалось получить данные" {
			t.Errorf("Message = %q, want %q", domainErr.Message, "не удалось получить данные")
		}

		// Проверяем цепочку причин через стандартный errors.Is
		if !errors.Is(domainErr, rootCause) {
			t.Error("errors.Is не нашел root cause в доменной ошибке")
		}
	})

	t.Run("Result корректно переносит доменные ошибки", func(t *testing.T) {
		// Симулируем бизнес-логику, которая возвращает Result с ошибкой
		result := Failure[string](ErrNotFound)

		if !result.IsErr() {
			t.Fatal("Ожидали ошибку в Result")
		}

		err := result.Err()
		if err == nil {
			t.Fatal("Err() вернул nil для ошибочного Result")
		}

		// Проверяем через наши хелперы
		if !IsNotFound(err) {
			t.Error("Хелпер IsNotFound не распознал ErrNotFound внутри Result")
		}
		if IsValidation(err) {
			t.Error("Хелпер IsValidation ложно сработал на ErrNotFound")
		}
	})

	t.Run("Хелперы проверки работают с обернутыми ошибками", func(t *testing.T) {
		// Симулируем ситуацию, когда доменная ошибка обернута в fmt.Errorf или другую обертку
		wrappedErr := fmt.Errorf("контекст операции: %w", ErrValidation)

		// Хелперы должны работать даже через обертки благодаря errors.As
		if !IsValidation(wrappedErr) {
			t.Error("IsValidation не распознал обернутую ErrValidation")
		}
		if IsNotFound(wrappedErr) {
			t.Error("IsNotFound ложно сработал на обернутую ErrValidation")
		}
	})

	t.Run("Success Result не имеет ошибок", func(t *testing.T) {
		result := Success[int](42)

		if !result.IsOk() {
			t.Fatal("Ожидали успешный Result")
		}
		if result.Err() != nil {
			t.Errorf("Ожидали nil ошибку для Success, получили: %v", result.Err())
		}
		if result.Value() != 42 {
			t.Errorf("Value() = %d, want 42", result.Value())
		}
	})
}
