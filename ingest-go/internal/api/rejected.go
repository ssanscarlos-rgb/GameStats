package api

import (
	"encoding/json"
	"sync"
	"time"
)

// Rejected es un evento que no pasó la validación, guardado con el motivo.
type Rejected struct {
	ReceivedAt time.Time       `json:"receivedAt"`
	EventID    string          `json:"eventId,omitempty"`
	MatchID    string          `json:"matchId,omitempty"`
	Problems   []string        `json:"problems"`
	Raw        json.RawMessage `json:"raw"`
}

// RejectedLog guarda los últimos eventos rechazados para poder consultarlos.
//
// Cada petición HTTP corre en su propia goroutine, así que varias pueden querer
// escribir aquí AL MISMO TIEMPO. El Mutex es un "candado": solo una goroutine
// puede tener el candado a la vez, y las demás esperan su turno.
type RejectedLog struct {
	mu    sync.Mutex
	items []Rejected
	limit int
	total int
}

func NewRejectedLog(limit int) *RejectedLog {
	return &RejectedLog{limit: limit}
}

func (l *RejectedLog) Add(r Rejected) {
	l.mu.Lock()
	defer l.mu.Unlock() // defer = "haz esto al salir de la función", aunque haya error

	l.total++
	l.items = append(l.items, r)
	if len(l.items) > l.limit { // solo guardamos los últimos N para no llenar la memoria
		l.items = l.items[len(l.items)-l.limit:]
	}
}

// Snapshot devuelve una copia, para que quien la lea no choque con escrituras nuevas.
func (l *RejectedLog) Snapshot() (items []Rejected, total int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Rejected(nil), l.items...), l.total
}
