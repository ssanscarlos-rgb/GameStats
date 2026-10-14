// Package api es la "puerta de entrada" HTTP del módulo de ingestión.
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ssanscarlos-rgb/gamestats/ingest-go/internal/event"
)

// Sink es "a quién le paso los eventos válidos". Por ahora será una función que
// los imprime; en el paso 3 será el pipeline concurrente. Así el servidor no
// necesita saber qué pasa después con los eventos.
type Sink func(event.Event)

type Server struct {
	sink     Sink
	rejected *RejectedLog
	status   func() any // opcional: qué mostrar en GET /status
}

func NewServer(sink Sink, rejected *RejectedLog) *Server {
	return &Server{sink: sink, rejected: rejected}
}

// WithStatus agrega una función que describe el estado interno (GET /status).
func (s *Server) WithStatus(f func() any) *Server {
	s.status = f
	return s
}

// Routes conecta cada dirección (ruta) con la función que la atiende.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /events", s.postEvents)
	mux.HandleFunc("GET /rejected", s.getRejected)
	mux.HandleFunc("GET /status", s.getStatus)
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// EventResult describe qué pasó con un evento rechazado dentro de un lote.
type EventResult struct {
	Index    int      `json:"index"`
	EventID  string   `json:"eventId,omitempty"`
	Problems []string `json:"problems"`
}

type postResponse struct {
	Accepted int           `json:"accepted"`
	Rejected int           `json:"rejected"`
	Errors   []EventResult `json:"errors,omitempty"`
}

// postEvents recibe eventos. Acepta tres formas, para que sea fácil de usar:
//
//	{"events":[...]}   igual que el formato que recibe Scala
//	[ {...}, {...} ]   un arreglo de eventos
//	{...}              un solo evento
//
// Cada evento se valida POR SEPARADO: uno malo no tumba a los demás
// (justo lo contrario de Scala, por eso esta validación existe).
func (s *Server) postEvents(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10<<20)) // máximo 10 MB
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "cuerpo demasiado grande o ilegible"})
		return
	}

	raws, err := splitEvents(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	resp := postResponse{}
	for i, raw := range raws {
		ev, problems := event.Parse(raw)
		if len(problems) > 0 {
			resp.Rejected++
			resp.Errors = append(resp.Errors, EventResult{Index: i, EventID: ev.EventID, Problems: problems})
			s.rejected.Add(Rejected{
				ReceivedAt: time.Now().UTC(), EventID: ev.EventID, MatchID: ev.MatchID,
				Problems: problems, Raw: raw,
			})
			continue
		}
		resp.Accepted++
		s.sink(ev)
	}

	// 202 Accepted = "lo recibí y lo voy a procesar", no "ya terminé de procesarlo".
	writeJSON(w, http.StatusAccepted, resp)
}

func (s *Server) getRejected(w http.ResponseWriter, _ *http.Request) {
	items, total := s.rejected.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "recent": items})
}

func (s *Server) getStatus(w http.ResponseWriter, _ *http.Request) {
	_, rejected := s.rejected.Snapshot()
	body := map[string]any{"rejected": rejected}
	if s.status != nil {
		body["pipeline"] = s.status()
	}
	writeJSON(w, http.StatusOK, body)
}

// splitEvents separa el cuerpo en eventos individuales SIN interpretarlos todavía.
func splitEvents(body []byte) ([]json.RawMessage, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, fmt.Errorf("el cuerpo está vacío")
	}

	switch body[0] {
	case '[':
		var list []json.RawMessage
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, fmt.Errorf("arreglo JSON inválido: %v", err)
		}
		return list, nil
	case '{':
		var wrapper struct {
			Events *[]json.RawMessage `json:"events"`
		}
		if err := json.Unmarshal(body, &wrapper); err != nil {
			return nil, fmt.Errorf("JSON inválido: %v", err)
		}
		if wrapper.Events != nil {
			return *wrapper.Events, nil
		}
		return []json.RawMessage{body}, nil // un solo evento
	default:
		return nil, fmt.Errorf("se esperaba un objeto o arreglo JSON")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
