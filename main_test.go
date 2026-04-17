package main

import (
	"bytes"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newTestApp(t *testing.T) *App {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	uploadDir := t.TempDir()
	app := &App{db: db, uploadDir: uploadDir}
	if err := app.initSchema(); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	return app
}

func TestUploadAddsPiroAndReturnsFeed(t *testing.T) {
	app := newTestApp(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	filePart, err := writer.CreateFormFile("video", "test.mp4")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = filePart.Write([]byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'm', 'p', '4', '2'})
	_ = writer.WriteField("tags", "beach, sunset")
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res := httptest.NewRecorder()

	app.handleUpload(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "beach, sunset") {
		t.Fatalf("expected returned feed to contain tags, got: %s", res.Body.String())
	}

	items, err := app.latest(15)
	if err != nil {
		t.Fatalf("latest query failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Duration != recordingSeconds {
		t.Fatalf("expected duration %d, got %d", recordingSeconds, items[0].Duration)
	}
	if !strings.HasPrefix(items[0].VideoPath, "/uploads/") {
		t.Fatalf("expected stored video path to be in /uploads, got %s", items[0].VideoPath)
	}

	stored := filepath.Join(app.uploadDir, strings.TrimPrefix(items[0].VideoPath, "/uploads/"))
	if _, err := os.Stat(stored); err != nil {
		t.Fatalf("expected uploaded file to exist on disk: %v", err)
	}
}

func TestUploadRejectsNonVideoFiles(t *testing.T) {
	app := newTestApp(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	filePart, err := writer.CreateFormFile("video", "not-video.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = filePart.Write([]byte("not a video"))
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res := httptest.NewRecorder()

	app.handleUpload(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "video") {
		t.Fatalf("expected validation error, got: %s", res.Body.String())
	}
}

func TestLatestReturnsMostRecent15(t *testing.T) {
	app := newTestApp(t)

	for i := 0; i < 20; i++ {
		err := app.insertPiro("/uploads/v.mp4", "tag", recordingSeconds)
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	items, err := app.latest(15)
	if err != nil {
		t.Fatalf("latest query failed: %v", err)
	}
	if len(items) != 15 {
		t.Fatalf("expected 15 items, got %d", len(items))
	}
	if items[0].ID <= items[len(items)-1].ID {
		t.Fatalf("expected descending order by newest first")
	}
}
