// Programa principal del módulo de ingestión: arma las piezas y las conecta.
//
//	servidor HTTP ──sink──► pipeline (trabajadores) ──► cliente analyzer ──► Scala
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ssanscarlos-rgb/gamestats/ingest-go/internal/analyzer"
	"github.com/ssanscarlos-rgb/gamestats/ingest-go/internal/api"
	"github.com/ssanscarlos-rgb/gamestats/ingest-go/internal/event"
	"github.com/ssanscarlos-rgb/gamestats/ingest-go/internal/pipeline"
)

func main() {
	// Configuración por variables de entorno (Docker Compose las define).
	port := getenv("PORT", "8080")
	analyzerURL := getenv("ANALYZER_URL", "http://localhost:8081")
	cfg := pipeline.Config{
		Workers:     getenvInt("WORKERS", 4),
		Senders:     getenvInt("SENDERS", 2),
		IdleTimeout: getenvDuration("IDLE_TIMEOUT", 30*time.Second),
	}

	rejected := api.NewRejectedLog(500)

	// Lo que el pipeline descarte (duplicados, tardíos...) va al mismo registro.
	onReject := func(e event.Event, problem string) {
		raw, _ := json.Marshal(e)
		rejected.Add(api.Rejected{
			ReceivedAt: time.Now().UTC(), EventID: e.EventID, MatchID: e.MatchID,
			Problems: []string{problem}, Raw: raw,
		})
	}

	pipe := pipeline.New(cfg, analyzer.New(analyzerURL), onReject)
	server := api.NewServer(pipe.Submit, rejected).
		WithStatus(func() any { return pipe.Stats() })

	httpServer := &http.Server{Addr: ":" + port, Handler: server.Routes()}

	// El servidor corre en su propia goroutine para que main pueda esperar la señal de apagado.
	go func() {
		log.Printf("ingest escuchando en :%s → análisis en %s (%d trabajadores, %d mensajeros, timeout %s)",
			port, analyzerURL, cfg.Workers, cfg.Senders, cfg.IdleTimeout)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	// Espera Ctrl+C (SIGINT) o la señal de apagado de Docker (SIGTERM).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	// Apagado ordenado: primero dejar de recibir, después vaciar el pipeline.
	log.Println("apagando: dejo de recibir eventos y envío las partidas pendientes...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	pipe.Close()
	log.Printf("listo. resumen: %+v", pipe.Stats())
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n > 0 {
		return n
	}
	return def
}

func getenvDuration(key string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(key)); err == nil && d > 0 {
		return d
	}
	return def
}
