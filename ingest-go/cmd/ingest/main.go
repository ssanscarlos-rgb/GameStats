// Programa principal del módulo de ingestión: arranca el servidor HTTP.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/ssanscarlos-rgb/gamestats/ingest-go/internal/api"
	"github.com/ssanscarlos-rgb/gamestats/ingest-go/internal/event"
)

func main() {
	port := getenv("PORT", "8080")

	// Paso 2: por ahora los eventos válidos solo se imprimen.
	// En el paso 3 esta función se reemplaza por el pipeline concurrente.
	printSink := func(e event.Event) {
		log.Printf("aceptado: %s  partida=%s  tipo=%s", e.EventID, e.MatchID, e.Type)
	}

	server := api.NewServer(printSink, api.NewRejectedLog(500))

	log.Printf("ingest escuchando en :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, server.Routes()))
}

// getenv lee una variable de entorno, o usa un valor por defecto si no existe.
// Así Docker puede cambiar el puerto sin tocar el código.
func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
