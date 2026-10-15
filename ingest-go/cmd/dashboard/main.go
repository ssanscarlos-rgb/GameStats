// Panel de GameStats: una página web que muestra el sistema funcionando en vivo
// y permite lanzar el simulador con un botón.
//
// El panel no analiza nada. Hace tres cosas:
//
//   - Sirve la página (web/index.html, incluida dentro del binario con go:embed).
//
//   - Reenvía las consultas de la página a ingest (Go) y a analytics (Scala).
//     Así el navegador habla con un solo origen y no hacen falta permisos CORS.
//
//   - Lanza y detiene el simulador como un proceso aparte, y guarda su salida.
//
//     página ──► /api/ingest/...   ──► ingest   :8080  (GET /status, /rejected, /health)
//     ──► /api/analyzer/... ──► analytics :8081 (GET /results/..., /health)
//     ──► /api/sim/...      ──► proceso del simulador ──POST /events──► ingest
//
//     go run ./cmd/dashboard          # y abrir http://localhost:8090
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed web
var webFiles embed.FS

func main() {
	port := getenv("PORT", "8090")
	ingestURL := getenv("INGEST_URL", "http://localhost:8080")
	analyzerURL := getenv("ANALYZER_URL", "http://localhost:8081")

	sim := newSimRunner(ingestURL, os.Getenv("SIMULATOR_BIN"))

	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           routes(ingestURL, analyzerURL, sim),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("panel en http://localhost:%s (ingest %s, analytics %s)", port, ingestURL, analyzerURL)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Println("apagando el panel...")
	sim.StopAndWait(10 * time.Second) // si había una simulación corriendo, se detiene con el panel
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

// routes arma todas las rutas del panel. Está separada de main para poder probarla.
func routes(ingestURL, analyzerURL string, sim *simRunner) http.Handler {
	mux := http.NewServeMux()

	page, _ := fs.Sub(webFiles, "web")
	mux.Handle("GET /", http.FileServerFS(page))

	// Solo GET: la página consulta, nunca escribe en ingest ni en Scala.
	// Sin esta restricción, el panel serviría de puente para mandar eventos.
	mux.Handle("GET /api/ingest/", proxyTo("/api/ingest", ingestURL))
	mux.Handle("GET /api/analyzer/", proxyTo("/api/analyzer", analyzerURL))

	mux.HandleFunc("GET /api/sim", sim.handleState)
	mux.HandleFunc("POST /api/sim/start", sim.handleStart)
	mux.HandleFunc("POST /api/sim/stop", sim.handleStop)

	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ingest": ingestURL, "analyzer": analyzerURL})
	})
	return mux
}

// proxyTo reenvía /api/ingest/status → http://localhost:8080/status, por ejemplo.
// Si el servicio no responde, contesta 502 con un JSON corto en vez de esperar.
func proxyTo(prefix, target string) http.Handler {
	u, err := url.Parse(target)
	if err != nil {
		log.Fatalf("URL inválida %q: %v", target, err)
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	rp.Transport = &http.Transport{ // sin proxy del sistema: los servicios están en la misma red
		ResponseHeaderTimeout: 5 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
	rp.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "sin respuesta de " + target})
	}
	return http.StripPrefix(prefix, rp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
