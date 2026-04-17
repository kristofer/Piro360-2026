package main

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const recordingSeconds = 10

type Piro360 struct {
	ID        int64
	VideoPath string
	Tags      string
	CreatedAt time.Time
	Duration  int
}

type App struct {
	db        *sql.DB
	uploadDir string
}

func main() {
	if err := os.MkdirAll("uploads", 0o755); err != nil {
		log.Fatalf("failed to create uploads directory: %v", err)
	}

	db, err := sql.Open("sqlite3", "./piro360.db")
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	app := &App{db: db, uploadDir: "uploads"}
	if err := app.initSchema(); err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleHome)
	mux.HandleFunc("/feed", app.handleFeed)
	mux.HandleFunc("/upload", app.handleUpload)
	mux.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(app.uploadDir))))

	addr := ":8080"
	log.Printf("Piro360 running on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func (a *App) initSchema() error {
	_, err := a.db.Exec(`
CREATE TABLE IF NOT EXISTS piro360 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_path TEXT NOT NULL,
    tags TEXT NOT NULL DEFAULT '',
    duration_seconds INTEGER NOT NULL DEFAULT 10,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`)
	return err
}

func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	items, err := a.latest(15)
	if err != nil {
		http.Error(w, "failed to load feed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTemplate.Execute(w, items); err != nil {
		http.Error(w, "failed to render", http.StatusInternalServerError)
	}
}

func (a *App) handleFeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	items, err := a.latest(15)
	if err != nil {
		http.Error(w, "failed to load feed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := feedTemplate.Execute(w, items); err != nil {
		http.Error(w, "failed to render", http.StatusInternalServerError)
	}
}

func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(128 << 20); err != nil {
		http.Error(w, "invalid upload payload", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		http.Error(w, "video file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	name := safeName(header.Filename)
	if name == "" {
		name = "piro360"
	}
	filename := fmt.Sprintf("%d-%s", time.Now().UnixNano(), name)
	diskPath := filepath.Join(a.uploadDir, filename)

	out, err := os.Create(diskPath)
	if err != nil {
		http.Error(w, "failed to save upload", http.StatusInternalServerError)
		return
	}
	defer out.Close()

	if _, err := io.Copy(out, file); err != nil {
		http.Error(w, "failed to save upload", http.StatusInternalServerError)
		return
	}

	tags := strings.TrimSpace(r.FormValue("tags"))
	if err := a.insertPiro("/uploads/"+filename, tags, recordingSeconds); err != nil {
		http.Error(w, "failed to store upload", http.StatusInternalServerError)
		return
	}

	items, err := a.latest(15)
	if err != nil {
		http.Error(w, "failed to load feed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := feedTemplate.Execute(w, items); err != nil {
		http.Error(w, "failed to render", http.StatusInternalServerError)
	}
}

func (a *App) insertPiro(videoPath, tags string, duration int) error {
	if strings.TrimSpace(videoPath) == "" {
		return errors.New("video path is required")
	}
	_, err := a.db.Exec(`INSERT INTO piro360(video_path, tags, duration_seconds) VALUES (?, ?, ?)`, videoPath, tags, duration)
	return err
}

func (a *App) latest(limit int) ([]Piro360, error) {
	rows, err := a.db.Query(`SELECT id, video_path, tags, created_at, duration_seconds FROM piro360 ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Piro360, 0, limit)
	for rows.Next() {
		var item Piro360
		if err := rows.Scan(&item.ID, &item.VideoPath, &item.Tags, &item.CreatedAt, &item.Duration); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func safeName(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	base = strings.ReplaceAll(base, " ", "-")
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, base)
	return strings.Trim(base, "-.")
}

var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>Piro360</title>
  <script src="https://unpkg.com/htmx.org@1.9.12"></script>
  <style>
    body { font-family: sans-serif; margin: 2rem auto; max-width: 900px; padding: 0 1rem; }
    form { display: grid; gap: .75rem; border: 1px solid #ddd; padding: 1rem; border-radius: 8px; }
    .card { border: 1px solid #ddd; border-radius: 8px; padding: .75rem; margin: .75rem 0; }
    video { width: 100%; max-height: 280px; background: #000; border-radius: 6px; }
    .muted { color: #666; font-size: .9rem; }
  </style>
</head>
<body>
  <h1>Piro360</h1>
  <p>Record a 10-second 360° pirouette and auto-upload it for the world to see.</p>

  <form hx-post="/upload" hx-target="#feed" hx-swap="innerHTML" enctype="multipart/form-data">
    <label>Video file (10s capture from the app)
      <input type="file" name="video" accept="video/*" required>
    </label>
    <label>Tags
      <input type="text" name="tags" placeholder="empire-state, nyc, skyline">
    </label>
    <button type="submit">Upload Piro360</button>
  </form>

  <h2>Latest 15 Piro360s</h2>
  <section id="feed">` + feedMarkup + `</section>
</body>
</html>`))

const feedMarkup = `{{if .}}{{range .}}
<div class="card">
  <video controls preload="metadata" src="{{.VideoPath}}"></video>
  <div><strong>Tags:</strong> {{if .Tags}}{{.Tags}}{{else}}(none){{end}}</div>
  <div class="muted">Recorded {{.Duration}}s · Uploaded {{.CreatedAt.Format "2006-01-02 15:04:05"}}</div>
</div>
{{end}}{{else}}<p class="muted">No Piro360 uploads yet. Be the first!</p>{{end}}`

var feedTemplate = template.Must(template.New("feed").Parse(feedMarkup))
