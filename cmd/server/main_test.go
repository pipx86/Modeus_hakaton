package main

import (
	"context"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SECRET", "")
	t.Setenv("HTTP_ADDR", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("missing configuration accepted")
	}
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("JWT_SECRET", "test-token")
	c, err := loadConfig()
	if err != nil || c.address != "127.0.0.1:8080" {
		t.Fatalf("config: %+v, %v", c, err)
	}
	t.Setenv("HTTP_ADDR", "127.0.0.1:9090")
	c, err = loadConfig()
	if err != nil || c.address != "127.0.0.1:9090" {
		t.Fatalf("custom address: %+v, %v", c, err)
	}
}
func TestRunRejectsBadDatabase(t *testing.T) {
	err := run(context.Background(), config{address: "127.0.0.1:0", databaseURL: "not-a-postgres-url", jwtSecret: "test-token"})
	if err == nil || strings.Contains(err.Error(), "not-a-postgres-url") {
		t.Fatalf("unexpected error: %v", err)
	}
}
