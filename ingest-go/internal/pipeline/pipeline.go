// Package pipeline es el corazón concurrente de la ingestión.
//
// Recorrido de un evento:
//
//	Submit ──► canal del trabajador N ──► trabajador N (agrupa por partida)
//	                                           │ partida lista
//	                                           ▼
//	                                      canal ready ──► mensajeros ──► Scala
//
// Cada partida SIEMPRE va al mismo trabajador (se elige con un hash del matchId).
// Así cada trabajador es el ÚNICO dueño de sus partidas: no comparte datos con
// nadie y no necesita candados. Se comunican solo por canales.
package pipeline

import (
	"context"
	"hash/fnv"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ssanscarlos-rgb/gamestats/ingest-go/internal/event"
)

// Sender es quien entrega una partida completa al módulo de análisis (Scala).
// Es una interfaz para poder usar uno falso en las pruebas.
type Sender interface {
	Send(ctx context.Context, matchID string, events []event.Event) error
}

// RejectFunc se llama cuando el pipeline descarta un evento (duplicado, tardío...).
type RejectFunc func(e event.Event, problem string)

type Config struct {
	Workers     int           // trabajadores que agrupan partidas
	Senders     int           // mensajeros que envían a Scala
	IdleTimeout time.Duration // partida sin eventos durante este tiempo = se da por terminada
	TickEvery   time.Duration // cada cuánto los trabajadores revisan partidas inactivas
	QueueSize   int           // capacidad de cada canal
}

// Batch es una partida lista para analizar.
type Batch struct {
	MatchID string
	GameID  string
	Reason  string // "finished" (llegó MATCH_FINISHED), "timeout" o "shutdown"
	Players int    // cuántos jugadores distintos participaron
	Events  []event.Event
}

// Stats son contadores que se pueden leer en cualquier momento (GET /status).
type Stats struct {
	Received      int64 `json:"received"`
	Duplicates    int64 `json:"duplicates"`
	Late          int64 `json:"late"`
	GameMismatch  int64 `json:"gameMismatch"`
	OpenMatches   int64 `json:"openMatches"`
	BatchesSent   int64 `json:"batchesSent"`
	BatchesFailed int64 `json:"batchesFailed"`
	Reopened      int64 `json:"reopened"`
	EventsSent    int64 `json:"eventsSent"`

	// Games cuenta las partidas por juego (gameId). Es la parte de GameStats v1.1
	// que usa el panel de GameConsole para mostrar "instancias por juego".
	Games map[string]GameCount `json:"games"`
}

// GameCount son las instancias de un juego desde que arrancó el ingest.
type GameCount struct {
	Opened    int64 `json:"opened"`    // partidas abiertas (incluye las que siguen en curso)
	Delivered int64 `json:"delivered"` // partidas ya entregadas a Scala con éxito
}

// gameCounters guarda un GameCount por gameId. Lo tocan varios trabajadores y
// mensajeros a la vez, así que va protegido con un candado.
type gameCounters struct {
	mu sync.Mutex
	m  map[string]*GameCount
}

func (g *gameCounters) add(gameID string, opened, delivered int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.m == nil {
		g.m = map[string]*GameCount{}
	}
	c := g.m[gameID]
	if c == nil {
		c = &GameCount{}
		g.m[gameID] = c
	}
	c.Opened += opened
	c.Delivered += delivered
}

// snapshot devuelve una copia: quien la lea no compite con quien sigue sumando.
func (g *gameCounters) snapshot() map[string]GameCount {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]GameCount, len(g.m))
	for id, c := range g.m {
		out[id] = *c
	}
	return out
}

// forgetMsg avisa a un trabajador que el envío de una partida falló.
type forgetMsg struct{ matchID, gameID string }

type Pipeline struct {
	cfg    Config
	inputs []chan event.Event // un canal por trabajador
	forget []chan forgetMsg   // avisos "esta partida falló, vuelve a abrirla", uno por trabajador
	ready  chan Batch         // partidas listas → mensajeros
	sender Sender
	reject RejectFunc
	games  gameCounters // instancias por juego (A6)

	workersWG sync.WaitGroup
	sendersWG sync.WaitGroup

	// Contadores atómicos: varios goroutines los suman a la vez sin candado.
	received, duplicates, late, mismatch, open, sent, failed, reopened, eventsSent atomic.Int64
}

// New crea el pipeline y ARRANCA todas sus goroutines.
func New(cfg Config, sender Sender, reject RejectFunc) *Pipeline {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.Senders <= 0 {
		cfg.Senders = 2
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 30 * time.Second
	}
	if cfg.TickEvery <= 0 {
		cfg.TickEvery = time.Second
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1024
	}
	if reject == nil {
		reject = func(event.Event, string) {}
	}

	p := &Pipeline{
		cfg:    cfg,
		inputs: make([]chan event.Event, cfg.Workers),
		forget: make([]chan forgetMsg, cfg.Workers),
		ready:  make(chan Batch, cfg.QueueSize),
		sender: sender,
		reject: reject,
	}

	for i := range p.inputs {
		p.inputs[i] = make(chan event.Event, cfg.QueueSize)
		p.forget[i] = make(chan forgetMsg, cfg.QueueSize)
		p.workersWG.Add(1)
		go p.runWorker(p.inputs[i], p.forget[i]) // "go" = arranca esta función en una goroutine nueva
	}
	for i := 0; i < cfg.Senders; i++ {
		p.sendersWG.Add(1)
		go p.runSender()
	}
	return p
}

// Submit mete un evento ya validado al pipeline. Es el "sink" del servidor HTTP.
func (p *Pipeline) Submit(e event.Event) {
	p.received.Add(1)
	p.inputs[p.shard(e.MatchID)] <- e
}

// shard decide qué trabajador atiende una partida. El mismo matchId da
// siempre el mismo número, así todos los eventos de una partida llegan juntos.
func (p *Pipeline) shard(matchID string) int {
	h := fnv.New32a()
	h.Write([]byte(matchID))
	return int(h.Sum32() % uint32(len(p.inputs)))
}

// Close apaga todo en orden y sin perder datos:
// 1) cierra la entrada de los trabajadores, que envían las partidas abiertas;
// 2) cuando todos terminan, cierra ready; 3) espera a que los mensajeros vacíen la cola.
// Hay que dejar de llamar a Submit antes de llamar a Close.
func (p *Pipeline) Close() {
	for _, ch := range p.inputs {
		close(ch)
	}
	p.workersWG.Wait()
	close(p.ready)
	p.sendersWG.Wait()
}

func (p *Pipeline) Stats() Stats {
	return Stats{
		Received: p.received.Load(), Duplicates: p.duplicates.Load(), Late: p.late.Load(),
		GameMismatch: p.mismatch.Load(), OpenMatches: p.open.Load(),
		BatchesSent: p.sent.Load(), BatchesFailed: p.failed.Load(), Reopened: p.reopened.Load(), EventsSent: p.eventsSent.Load(),
		Games: p.games.snapshot(),
	}
}

// runSender es un mensajero: toma partidas listas de la cola y las envía a Scala.
// "for b := range p.ready" repite hasta que alguien cierre el canal.
func (p *Pipeline) runSender() {
	defer p.sendersWG.Done()
	for b := range p.ready {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err := p.sender.Send(ctx, b.MatchID, b.Events)
		cancel()
		if err != nil {
			// La partida no llegó a Scala: se le avisa a su trabajador para que
			// la olvide como "cerrada" y un reenvío de sus eventos se acepte.
			p.reopen(b.MatchID, b.GameID)
			p.failed.Add(1)
			log.Printf("ERROR enviando partida %s (%d eventos): %v", b.MatchID, len(b.Events), err)
			continue
		}
		p.sent.Add(1)
		p.games.add(b.GameID, 0, 1)
		p.eventsSent.Add(int64(len(b.Events)))
		log.Printf("partida %s (%s) enviada a análisis: %d eventos, %d jugadores [%s]", b.MatchID, b.GameID, len(b.Events), b.Players, b.Reason)
	}
}

// reopen avisa al trabajador dueño de la partida que el envío falló.
// El select con default nunca bloquea: si la cola de avisos está llena o el
// trabajador ya terminó (apagado), el aviso se descarta en vez de trabar al mensajero.
func (p *Pipeline) reopen(matchID, gameID string) {
	select {
	case p.forget[p.shard(matchID)] <- forgetMsg{matchID: matchID, gameID: gameID}:
	default:
	}
}
