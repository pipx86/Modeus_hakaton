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
