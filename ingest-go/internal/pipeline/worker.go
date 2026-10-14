package pipeline

import (
	"fmt"
	"time"

	"github.com/ssanscarlos-rgb/gamestats/ingest-go/internal/event"
)

// matchState es la "cajita" de una partida abierta. Solo la toca su trabajador.
type matchState struct {
	gameID   string
	events   []event.Event
	seen     map[string]bool // eventIds ya recibidos, para descartar duplicados
	players  map[string]bool // jugadores que han participado
	lastSeen time.Time       // cuándo llegó el último evento (para el timeout)
}

// runWorker es un trabajador. Vive mientras su canal esté abierto y atiende
// tres cosas con select: eventos nuevos, el "tic" del reloj para revisar
// inactividad, y avisos de partidas cuyo envío a Scala falló.
func (p *Pipeline) runWorker(in <-chan event.Event, forget <-chan string) {
	defer p.workersWG.Done()

	open := map[string]*matchState{}       // partidas abiertas de ESTE trabajador
	closed := map[string]map[string]bool{} // partidas ya enviadas → sus eventIds
	ticker := time.NewTicker(p.cfg.TickEvery)
	defer ticker.Stop()

	for {
		// select espera a que pase lo primero de varias cosas.
		select {
		case e, ok := <-in:
			if !ok { // canal cerrado = el programa se está apagando
				for id, m := range open {
					p.emit(id, m, "shutdown")
				}
				return
			}
			p.handle(e, open, closed)

		case id := <-forget:
			// El envío falló: la partida deja de contar como cerrada. Si sus
			// eventos se reenvían, se abren en una cajita nueva y se reintenta.
			if _, ok := closed[id]; ok {
				delete(closed, id)
				p.reopened.Add(1)
			}

		case now := <-ticker.C:
			for id, m := range open {
				if now.Sub(m.lastSeen) >= p.cfg.IdleTimeout {
					p.emit(id, m, "timeout")
					delete(open, id)
					closed[id] = m.seen
				}
			}
		}
	}
}

// handle aplica las reglas que necesitan ESTADO (recordar eventos anteriores),
// algo que la validación individual del paso 1 no puede hacer.
func (p *Pipeline) handle(e event.Event, open map[string]*matchState, closed map[string]map[string]bool) {
	if seen, isClosed := closed[e.MatchID]; isClosed {
		if seen[e.EventID] { // el mismo evento otra vez: es un duplicado, no un tardío
			p.duplicates.Add(1)
			p.reject(e, "eventId duplicado: ya se había recibido")
			return
		}
		p.late.Add(1)
		p.reject(e, fmt.Sprintf("la partida %s ya fue cerrada y enviada a análisis", e.MatchID))
		return
	}

	m := open[e.MatchID]
	if m == nil { // primer evento de esta partida: se abre su cajita
		m = &matchState{gameID: e.GameID, seen: map[string]bool{}, players: map[string]bool{}}
		open[e.MatchID] = m
		p.open.Add(1)
	}

	if e.GameID != m.gameID {
		p.mismatch.Add(1)
		p.reject(e, fmt.Sprintf("la partida %s pertenece a %q, no a %q", e.MatchID, m.gameID, e.GameID))
		return
	}
	if m.seen[e.EventID] {
		p.duplicates.Add(1)
		p.reject(e, "eventId duplicado: ya se había recibido")
		return
	}

	m.seen[e.EventID] = true
	m.events = append(m.events, e)
	m.lastSeen = time.Now()
	if e.PlayerID != nil {
		m.players[*e.PlayerID] = true
	}

	if e.Type == event.TypeMatchFinished {
		p.emit(e.MatchID, m, "finished")
		delete(open, e.MatchID)
		closed[e.MatchID] = m.seen
	}
}

// emit pone la partida en la cola de los mensajeros.
func (p *Pipeline) emit(matchID string, m *matchState, reason string) {
	p.open.Add(-1)
	p.ready <- Batch{MatchID: matchID, GameID: m.gameID, Reason: reason, Players: len(m.players), Events: m.events}
}
