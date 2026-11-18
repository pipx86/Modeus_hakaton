package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
)

func uploadSource(t *testing.T, baseURL, token, filename string, content []byte, fields map[string]string) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("field: %v", err)
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("file part: %v", err)
	}
	if _, err = part.Write(content); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err = writer.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/sources/upload", &body)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func TestUploadSourceFromTextFile(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	token, _ := auth.Issue("teacher-user", auth.RoleTeacher, time.Hour)

	resp := uploadSource(t, ts.URL, token, "role.md", []byte("# Роль\nОписание роли"), map[string]string{
		"type": "standard",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("ожидали 201, получили %d", resp.StatusCode)
	}
	var source domain.Source
	if err := json.NewDecoder(resp.Body).Decode(&source); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if source.Title != "role" {
		t.Fatalf("заголовок из имени файла: %q", source.Title)
	}
	if source.Type != domain.SourceStandard {
		t.Fatalf("тип источника: %q", source.Type)
	}
	if source.Content == "" {
		t.Fatal("текст файла не сохранился")
	}
}

func TestUploadSourceUsesTitleField(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	token, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)

	resp := uploadSource(t, ts.URL, token, "notes.txt", []byte("текст"), map[string]string{
		"title": "Профстандарт аналитика",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("ожидали 201, получили %d", resp.StatusCode)
	}
	var source domain.Source
	json.NewDecoder(resp.Body).Decode(&source)
	if source.Title != "Профстандарт аналитика" {
		t.Fatalf("заголовок: %q", source.Title)
	}
}

func TestUploadSourceRejectsUnsupportedFile(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	token, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)

	resp := uploadSource(t, ts.URL, token, "image.png", []byte("png"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("ожидали 400, получили %d", resp.StatusCode)
	}
}

func TestUploadSourceForbiddenForGuest(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	token, _ := auth.Issue("guest-user", auth.RoleGuest, time.Hour)

	resp := uploadSource(t, ts.URL, token, "role.txt", []byte("текст"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ожидали 403, получили %d", resp.StatusCode)
	}
}

func TestUploadSourceRequiresFile(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	token, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("type", "document")
	_ = writer.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/sources/upload", &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("ожидали 400, получили %d", resp.StatusCode)
	}
}
