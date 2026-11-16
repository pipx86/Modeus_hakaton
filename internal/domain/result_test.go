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

func TestResultCheckers(t *testing.T) {
	okRes := Success("data")
	if !okRes.IsOk() {
		t.Fatal("Success должен быть IsOk")
	}
	if okRes.IsErr() {
		t.Fatal("Success не должен быть IsErr")
	}

	errRes := Failure[string](errors.New("fail"))
	if errRes.IsOk() {
		t.Fatal("Failure не должен быть IsOk")
	}
	if !errRes.IsErr() {
		t.Fatal("Failure должен быть IsErr")
	}
}

func TestResultValueAndErr(t *testing.T) {
	// Проверяем безопасный доступ к значению и ошибке
	okRes := Success(100)
	if okRes.Value() != 100 {
		t.Fatalf("ожидали 100, получили %d", okRes.Value())
	}
	if okRes.Err() != nil {
		t.Fatal("у Success не должно быть ошибки")
	}

	domainErr := NewDomainError("TEST", "test msg", nil)
	errRes := Failure[int](domainErr)
	var zero int
	if errRes.Value() != zero {
		t.Fatal("у Failure значение должно быть zero value")
	}
	if errRes.Err() != domainErr {
		t.Fatal("ошибка не совпадает")
	}
}

func TestResultMap(t *testing.T) {
	// Map должен применять функцию только к успешному результату
	res := Success(5).Map(func(v int) int { return v * 2 })
	if res.Value() != 10 {
		t.Fatalf("ожидали 10, получили %d", res.Value())
	}

	// Map должен сохранять ошибку
	errRes := Failure[int](errors.New("fail")).Map(func(v int) int { return v * 2 })
	if !errRes.IsErr() {
		t.Fatal("Map не сохранил ошибку")
	}
}

func TestResultAndThen(t *testing.T) {
	// AndThen позволяет строить цепочки
	res := Success(5).AndThen(func(v int) Result[int] {
		return Success(v + 5)
	})
	if res.Value() != 10 {
		t.Fatalf("ожидали 10, получили %d", res.Value())
	}

	// AndThen прерывает цепочку при ошибке
	errRes := Failure[int](errors.New("fail")).AndThen(func(v int) Result[int] {
		return Success(v + 5) // Эта функция не должна вызваться
	})
	if !errRes.IsErr() {
		t.Fatal("AndThen не прервал цепочку")
	}
}

func TestDomainErrorUnwrap(t *testing.T) {
	cause := errors.New("root cause")
	dErr := NewDomainError("CODE", "msg", cause)

	// Проверяем, что Unwrap работает для errors.Is / errors.As
	if !errors.Is(dErr, cause) {
		t.Fatal("errors.Is не нашел причину")
	}

	var target *Error
	if !errors.As(dErr, &target) {
		t.Fatal("errors.As не привел к типу Error")
	}
	if target.Code != "CODE" {
		t.Fatal("код ошибки потерян при As")
	}
}
