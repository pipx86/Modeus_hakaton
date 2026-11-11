package domain

import (
	"errors"
	"testing"
)

func TestResultAndDomainError(t *testing.T) {
	value, err := Success(42).Unwrap()
	if value != 42 || err != nil {
		t.Fatalf("%d %v", value, err)
	}
	cause := errors.New("источник отсутствует")
	domainErr := &Error{Code: "SOURCE_NOT_FOUND", Message: "источник не найден", Cause: cause}
	_, err = Failure[int](domainErr).Unwrap()
	var actual *Error
	if !errors.As(err, &actual) || actual.Code != "SOURCE_NOT_FOUND" || !errors.Is(err, cause) {
		t.Fatalf("ошибка потеряна: %v", err)
	}
	var empty Result[int]
	_, err = empty.Unwrap()
	if !errors.Is(err, ErrUninitializedResult) {
		t.Fatal("неинициализированный результат принят")
	}
	_, err = Failure[int](nil).Unwrap()
	if err == nil {
		t.Fatal("ошибка не может быть пустой")
	}
}
