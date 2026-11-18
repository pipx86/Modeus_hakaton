package main

import (
	"context"
	"errors"
	"testing"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

type fakeZunClient struct {
	answer string
	err    error
}

func (f fakeZunClient) Chat(context.Context, []llm.Message) (string, error) {
	return f.answer, f.err
}

func TestEnrichZunAttachesValues(t *testing.T) {
	nodes := []domain.Node{{ID: "n1", Type: domain.NodeTask, Title: "Писать SQL-запросы"}}
	client := fakeZunClient{answer: `{"n1": {"knowledge": ["SQL"], "skills": ["писать запросы"], "abilities": ["аналитическое мышление"]}}`}

	out := enrichZun(context.Background(), client, "аналитик", nodes)

	if out[0].Zun == nil {
		t.Fatal("ожидали заполненный ЗУН")
	}
	if len(out[0].Zun.Knowledge) != 1 || out[0].Zun.Knowledge[0] != "SQL" {
		t.Fatalf("неожиданные знания: %#v", out[0].Zun.Knowledge)
	}
	if out[0].Zun.Abilities[0] != "аналитическое мышление" {
		t.Fatalf("неожиданные навыки: %#v", out[0].Zun.Abilities)
	}
}

func TestEnrichZunStripsCodeFence(t *testing.T) {
	nodes := []domain.Node{{ID: "n1", Type: domain.NodeAction, Title: "Собрать отчёт"}}
	client := fakeZunClient{answer: "```json\n{\"n1\": {\"knowledge\": [], \"skills\": [\"собирать отчёты\"], \"abilities\": []}}\n```"}

	out := enrichZun(context.Background(), client, "аналитик", nodes)

	if out[0].Zun == nil || len(out[0].Zun.Skills) != 1 {
		t.Fatalf("ожидали ЗУН после снятия markdown-обёртки, получили %#v", out[0].Zun)
	}
}

func TestEnrichZunIgnoresBrokenAnswer(t *testing.T) {
	nodes := []domain.Node{{ID: "n1", Type: domain.NodeTask, Title: "Задача"}}

	out := enrichZun(context.Background(), fakeZunClient{answer: "не json"}, "аналитик", nodes)
	if out[0].Zun != nil {
		t.Fatalf("при битом ответе ЗУН должен остаться пустым")
	}

	out = enrichZun(context.Background(), fakeZunClient{err: errors.New("timeout")}, "аналитик", nodes)
	if out[0].Zun != nil {
		t.Fatalf("при ошибке модели ЗУН должен остаться пустым")
	}
}
