package main

import (
	"embed"
	"html/template"
	"net/http"
	"strings"
)

//go:embed templates/*
var templatesFS embed.FS

// Handler handles HTTP requests for the UI.
type Handler struct {
	inspector *Inspector
	templates map[string]*template.Template
}

// NewHandler creates a new Handler.
func NewHandler(inspector *Inspector) (*Handler, error) {
	funcMap := template.FuncMap{
		"add": func(a, b int64) int64 { return a + b },
	}

	pages := []string{"dashboard.html", "queues.html", "tasks.html"}
	templates := make(map[string]*template.Template)

	for _, page := range pages {
		tmpl := template.New("base.html").Funcs(funcMap)
		// Parse base.html + the specific page
		if _, err := tmpl.ParseFS(templatesFS, "templates/base.html", "templates/"+page); err != nil {
			return nil, err
		}
		templates[page] = tmpl
	}

	return &Handler{
		inspector: inspector,
		templates: templates,
	}, nil
}

// RegisterRoutes registers HTTP routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", h.handleDashboard)
	mux.HandleFunc("/queues", h.handleQueues)
	mux.HandleFunc("/queues/", h.handleQueueTasks)
	mux.HandleFunc("/api/stats", h.handleAPIStats)
}

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	// ... func continues ... (rest of logic same, just render call changes slightly)

	stats, err := h.inspector.GetDashboardStats(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	queues, _ := h.inspector.GetQueues(r.Context())
	servers, _ := h.inspector.GetServers(r.Context())

	data := map[string]interface{}{
		"Stats":   stats,
		"Queues":  queues,
		"Servers": servers,
		"Page":    "dashboard",
	}

	h.render(w, "dashboard.html", data)
}

func (h *Handler) handleQueues(w http.ResponseWriter, r *http.Request) {
	queues, err := h.inspector.GetQueues(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := map[string]interface{}{
		"Queues": queues,
		"Page":   "queues",
	}

	h.render(w, "queues.html", data)
}

func (h *Handler) handleQueueTasks(w http.ResponseWriter, r *http.Request) {
	// Extract queue name from path: /queues/{name}
	path := strings.TrimPrefix(r.URL.Path, "/queues/")
	parts := strings.Split(path, "/")
	qname := parts[0]

	if qname == "" {
		http.Redirect(w, r, "/queues", http.StatusFound)
		return
	}

	state := r.URL.Query().Get("state")
	if state == "" {
		state = "pending"
	}

	tasks, err := h.inspector.GetTasks(r.Context(), qname, state, 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	queueInfo, _ := h.inspector.getQueueInfo(r.Context(), qname)

	data := map[string]interface{}{
		"Queue": queueInfo,
		"Tasks": tasks,
		"State": state,
		"Page":  "tasks",
	}

	h.render(w, "tasks.html", data)
}

func (h *Handler) handleAPIStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.inspector.GetDashboardStats(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"total_queues":` + itoa(stats.TotalQueues) +
		`,"total_pending":` + itoa64(stats.TotalPending) +
		`,"active_servers":` + itoa(stats.ActiveServers) + `}`))
}

func (h *Handler) render(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl, ok := h.templates[name]
	if !ok {
		http.Error(w, "Template not found: "+name, http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "base.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func itoa(n int) string {
	return strings.TrimSpace(strings.Replace(string(rune('0'+n%10)), "\x00", "", -1))
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var result []byte
	for n > 0 {
		result = append([]byte{byte('0' + n%10)}, result...)
		n /= 10
	}
	return string(result)
}
