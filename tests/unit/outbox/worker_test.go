package outbox_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"activity-descriptor/internal/outbox"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
)

type senderFunc func(context.Context, []byte) error

func (send senderFunc) Send(ctx context.Context, data []byte) error { return send(ctx, data) }

func TestDeliveryCommitsStatusAndSchedulesRetry(t *testing.T) {
	const eventID = "805cd798-35d0-4637-ab57-00d9cd706624"
	data := []byte(`{"event":"run.completed"}`)
	deliveryErr := errors.New("тестовый отказ получателя")
	dbErr := errors.New("тестовый сбой записи")
	for _, tc := range []struct {
		name      string
		sendErr   error
		updateErr error
		want      error
	}{
		{"доставлено", nil, nil, nil},
		{"перенесено", deliveryErr, nil, deliveryErr},
		{"откат обновления", nil, dbErr, dbErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, err := pgxmock.NewPool()
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			retry := 25 * time.Second
			delivered := 0
			w := outbox.Worker{Pool: pool, RetryDelay: retry, Sender: senderFunc(func(_ context.Context, got []byte) error {
				if !bytes.Equal(got, data) {
					t.Fatalf("тело события: %q", got)
				}
				delivered++
				return tc.sendErr
			})}
			pool.ExpectBegin()
			pool.ExpectQuery("FOR UPDATE SKIP LOCKED").
				WillReturnRows(pgxmock.NewRows([]string{"id", "payload"}).AddRow(eventID, data))
			var update *pgxmock.ExpectedExec
			if tc.sendErr != nil {
				update = pool.ExpectExec("SET status='failed'").WithArgs(eventID, retry.Microseconds())
			} else {
				update = pool.ExpectExec("SET status='sent'").WithArgs(eventID)
			}
			if tc.updateErr != nil {
				update.WillReturnError(tc.updateErr)
				pool.ExpectRollback()
			} else {
				update.WillReturnResult(pgxmock.NewResult("UPDATE", 1))
				pool.ExpectCommit()
			}
			found, err := w.DeliverOne(context.Background())
			if !found || delivered != 1 || !errors.Is(err, tc.want) {
				t.Fatalf("доставка: найдено %v, вызовов %d, ошибка %v", found, delivered, err)
			}
			if err := pool.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDeliveryDoesNotSendWhenNoEventIsDue(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	w := outbox.Worker{Pool: pool, RetryDelay: time.Second, Sender: senderFunc(func(context.Context, []byte) error {
		t.Fatal("отправка без ожидающего события")
		return nil
	})}
	pool.ExpectBegin()
	pool.ExpectQuery("FOR UPDATE SKIP LOCKED").WillReturnError(pgx.ErrNoRows)
	pool.ExpectRollback()
	if found, err := w.DeliverOne(context.Background()); found || err != nil {
		t.Fatalf("пустая очередь: %v, %v", found, err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
