package domain

import "errors"

// Error описывает ожидаемую ошибку предметной области с устойчивым кодом.
type Error struct {
	Code    string
	Message string
	Cause   error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Cause }

var ErrUninitializedResult = errors.New("результат не был инициализирован")

// Result хранит либо значение, либо ошибку; существующие интерфейсы Go не меняются.
type Result[T any] struct {
	value       T
	err         error
	initialized bool
}

func Success[T any](value T) Result[T] { return Result[T]{value: value, initialized: true} }
func Failure[T any](err error) Result[T] {
	if err == nil {
		err = ErrUninitializedResult
	}
	return Result[T]{err: err, initialized: true}
}
func (r Result[T]) Unwrap() (T, error) {
	if !r.initialized {
		var zero T
		return zero, ErrUninitializedResult
	}
	return r.value, r.err
}

// IsOk возвращает true, если результат успешен.
func (r Result[T]) IsOk() bool {
	return r.initialized && r.err == nil
}

// IsErr возвращает true, если результат содержит ошибку.
func (r Result[T]) IsErr() bool {
	return !r.IsOk()
}

// Value безопасно возвращает значение. Если была ошибка, вернется zero value типа T.
func (r Result[T]) Value() T {
	return r.value
}

// Err возвращает ошибку. Если результат успешен, вернется nil.
func (r Result[T]) Err() error {
	return r.err
}

// Map применяет функцию к значению, если результат успешен.
func (r Result[T]) Map(fn func(T) T) Result[T] {
	if r.IsErr() {
		return r
	}
	return Success(fn(r.value))
}

// AndThen позволяет строить цепочки операций.
// Функция fn должна сама возвращать Result.
func (r Result[T]) AndThen(fn func(T) Result[T]) Result[T] {
	if r.IsErr() {
		return r
	}
	return fn(r.value)
}

//domain error
// NewDomainError создает типизированную ошибку домена.
func NewDomainError(code, message string, cause error) *Error {
	return &Error{
		Code:    code,
		Message: message,
		Cause:   cause,
	}
}

// Удобные константы для частых ошибок (пример)
var (
	ErrNotFound     = NewDomainError("NOT_FOUND", "сущность не найдена", nil)
	ErrValidation   = NewDomainError("VALIDATION_ERROR", "ошибка валидации данных", nil)
	ErrUnauthorized = NewDomainError("UNAUTHORIZED", "доступ запрещен", nil)
)
