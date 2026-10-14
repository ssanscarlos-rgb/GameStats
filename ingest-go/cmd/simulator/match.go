package main

import (
	"fmt"
	"math/rand/v2"
	"time"
)

// Event es un evento tal como lo produciría un juego externo.
//
// El simulador representa una FUENTE EXTERNA: a propósito no importa ningún
// paquete interno de ingest-go. Solo conoce el formato JSON del contrato
// (docs/CONTRACT.md) y habla con el servicio por HTTP, igual que un juego real.
type Event struct {
	EventID     string         `json:"eventId"`
	Timestamp   string         `json:"timestamp"`
	GameID      string         `json:"gameId"`
	GameVersion string         `json:"gameVersion"`
	MatchID     string         `json:"matchId"`
	PlayerID    string         `json:"playerId,omitempty"`
	Type        string         `json:"type"`
	Data        map[string]any `json:"data,omitempty"`
}

// match arma los eventos de UNA partida con su propio reloj simulado.
// El reloj avanza con wait(); cada add() estampa la hora actual del reloj.
type match struct {
	gameID  string
	matchID string
	clock   time.Time
	seq     int
	events  []Event
}

func newMatch(gameID, matchID string, start time.Time) *match {
	return &match{gameID: gameID, matchID: matchID, clock: start}
}

// wait adelanta el reloj de la partida (tiempo de juego, no tiempo real).
func (m *match) wait(d time.Duration) { m.clock = m.clock.Add(d) }

// add agrega un evento con la hora actual del reloj. player vacío = evento de partida.
func (m *match) add(player, typ string, data map[string]any) {
	m.seq++
	m.events = append(m.events, Event{
		EventID:     fmt.Sprintf("%s-%03d", m.matchID, m.seq),
		Timestamp:   m.clock.UTC().Format("2006-01-02T15:04:05.000Z"),
		GameID:      m.gameID,
		GameVersion: "1.0",
		MatchID:     m.matchID,
		PlayerID:    player,
		Type:        typ,
		Data:        data,
	})
}

// finish cierra la partida. winner vacío = nadie ganó.
func (m *match) finish(winner string) []Event {
	var data map[string]any
	if winner != "" {
		data = map[string]any{"winnerId": winner}
	}
	m.add("", "MATCH_FINISHED", data)
	return m.events
}

// --- utilidades de azar ---

var playerPool = []string{"ana", "luis", "sofi", "leo", "dani", "vale", "marco", "cami", "andres", "jean", "nati", "pablo"}

// pickPlayers elige n jugadores distintos al azar.
func pickPlayers(r *rand.Rand, n int) []string {
	idx := r.Perm(len(playerPool))[:n]
	out := make([]string, n)
	for i, j := range idx {
		out[i] = playerPool[j]
	}
	return out
}

// between devuelve una duración al azar entre min y max.
func between(r *rand.Rand, min, max time.Duration) time.Duration {
	return min + time.Duration(r.Int64N(int64(max-min)+1))
}
