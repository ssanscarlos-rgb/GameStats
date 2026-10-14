package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ssanscarlos-rgb/gamestats/ingest-go/internal/event"
)

// fakeSender hace de "Scala falso": guarda lo que recibe. Si failNext > 0,
// los próximos envíos fallan, como si Scala estuviera caído.
type fakeSender struct {
	mu       sync.Mutex
	batches  map[string][]event.Event
	failNext int
}

func (f *fakeSender) Send(_ context.Context, matchID string, evs []event.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext > 0 {
		f.failNext--
		return fmt.Errorf("Scala caído (simulado)")
	}
	f.batches[matchID] = evs
	return nil
}

func (f *fakeSender) get(id string) ([]event.Event, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	evs, ok := f.batches[id]
	return evs, ok
}

type rejections struct {
	mu   sync.Mutex
	list []string
}

func (r *rejections) add(e event.Event, problem string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, e.EventID+": "+problem)
}

func (r *rejections) all() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.list, "\n")
}

func newTest(cfg Config) (*Pipeline, *fakeSender, *rejections) {
	s := &fakeSender{batches: map[string][]event.Event{}}
	r := &rejections{}
	return New(cfg, s, r.add), s, r
}

func ev(match, id, typ string) event.Event {
	return event.Event{EventID: id, Timestamp: "2026-09-01T10:00:00Z", GameID: "combat", MatchID: match, Type: typ}
}

// Esperar algo que pasa en otra goroutine, con límite de tiempo.
func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("la condición nunca se cumplió")
}

func TestMatchIsSentWhenFinished(t *testing.T) {
	p, s, _ := newTest(Config{})
	defer p.Close()

	p.Submit(ev("m1", "1", "MATCH_STARTED"))
	p.Submit(ev("m1", "2", "SHOT_FIRED"))
	if _, sent := s.get("m1"); sent {
		t.Fatal("no debería enviarse antes de MATCH_FINISHED")
	}
	p.Submit(ev("m1", "3", "MATCH_FINISHED"))

	eventually(t, func() bool { evs, ok := s.get("m1"); return ok && len(evs) == 3 })
}

func TestDuplicatesAreDropped(t *testing.T) {
	p, s, r := newTest(Config{})
	p.Submit(ev("m1", "1", "MATCH_STARTED"))
	p.Submit(ev("m1", "1", "MATCH_STARTED")) // repetido
	p.Submit(ev("m1", "2", "MATCH_FINISHED"))
	p.Close()

	if evs, _ := s.get("m1"); len(evs) != 2 {
		t.Errorf("esperaba 2 eventos, llegaron %d", len(evs))
	}
	if !strings.Contains(r.all(), "duplicado") || p.Stats().Duplicates != 1 {
		t.Errorf("el duplicado debería registrarse: %q", r.all())
	}
}

func TestLateEventAfterFinishIsRejected(t *testing.T) {
	p, _, r := newTest(Config{})
	p.Submit(ev("m1", "1", "MATCH_FINISHED"))
	p.Submit(ev("m1", "2", "SHOT_FIRED"))
	p.Close()
	if !strings.Contains(r.all(), "ya fue cerrada") {
		t.Errorf("esperaba rechazo por tardío: %q", r.all())
	}
}

func TestResentFinishedMatchCountsAsDuplicate(t *testing.T) {
	p, _, _ := newTest(Config{})
	p.Submit(ev("m1", "1", "MATCH_STARTED"))
	p.Submit(ev("m1", "2", "MATCH_FINISHED"))
	p.Submit(ev("m1", "1", "MATCH_STARTED")) // reenvío del mismo evento
	p.Submit(ev("m1", "3", "SHOT_FIRED"))    // evento nuevo, pero tarde
	p.Close()
	if st := p.Stats(); st.Duplicates != 1 || st.Late != 1 {
		t.Errorf("esperaba 1 duplicado y 1 tardío: %+v", st)
	}
}

// Si el envío falla, la partida se puede reenviar completa cuando Scala vuelve.
func TestFailedMatchCanBeResent(t *testing.T) {
	p, s, r := newTest(Config{})
	s.failNext = 1 // el primer envío falla

	p.Submit(ev("m1", "1", "MATCH_STARTED"))
	p.Submit(ev("m1", "2", "MATCH_FINISHED"))
	eventually(t, func() bool { return p.Stats().Reopened == 1 })

	// "Scala volvió": se reenvían los mismos eventos y ahora sí llegan.
	p.Submit(ev("m1", "1", "MATCH_STARTED"))
	p.Submit(ev("m1", "2", "MATCH_FINISHED"))
	p.Close()

	if evs, ok := s.get("m1"); !ok || len(evs) != 2 {
		t.Fatalf("la partida reenviada debería llegar completa: %v", evs)
	}
	if st := p.Stats(); st.BatchesFailed != 1 || st.BatchesSent != 1 || st.Duplicates != 0 || r.all() != "" {
		t.Errorf("stats=%+v rechazos=%q", st, r.all())
	}
}

func TestGameMismatchIsRejected(t *testing.T) {
	p, _, r := newTest(Config{})
	p.Submit(ev("m1", "1", "MATCH_STARTED"))
	other := ev("m1", "2", "LAP_COMPLETED")
	other.GameID = "racing"
	p.Submit(other)
	p.Close()
	if !strings.Contains(r.all(), "pertenece a") {
		t.Errorf("esperaba rechazo por juego distinto: %q", r.all())
	}
}

// Una partida que nunca recibe MATCH_FINISHED se envía por inactividad.
func TestIdleMatchIsSentByTimeout(t *testing.T) {
	p, s, _ := newTest(Config{IdleTimeout: 50 * time.Millisecond, TickEvery: 10 * time.Millisecond})
	defer p.Close()
	p.Submit(ev("abandonada", "1", "MATCH_STARTED"))
	eventually(t, func() bool { _, ok := s.get("abandonada"); return ok })
}

// Al apagar, las partidas abiertas no se pierden.
func TestOpenMatchesAreFlushedOnClose(t *testing.T) {
	p, s, _ := newTest(Config{IdleTimeout: time.Hour})
	p.Submit(ev("abierta", "1", "MATCH_STARTED"))
	p.Close()
	if _, ok := s.get("abierta"); !ok {
		t.Error("la partida abierta debería enviarse al apagar")
	}
}

// Prueba de estrés: 50 partidas, eventos enviados desde muchas goroutines a la vez.
// Correr con "go test -race" para que Go detecte accesos simultáneos peligrosos.
func TestManyMatchesConcurrently(t *testing.T) {
	const matches, perMatch = 50, 20
	p, s, r := newTest(Config{Workers: 4, Senders: 3})

	var wg sync.WaitGroup
	for m := 0; m < matches; m++ {
		wg.Add(1)
		go func(m int) { // cada partida la "juega" una goroutine distinta
			defer wg.Done()
			id := fmt.Sprintf("m%02d", m)
			for i := 0; i < perMatch-1; i++ {
				p.Submit(ev(id, fmt.Sprintf("%s-%d", id, i), "SHOT_FIRED"))
			}
			p.Submit(ev(id, id+"-fin", "MATCH_FINISHED"))
		}(m)
	}
	wg.Wait()
	p.Close()

	for m := 0; m < matches; m++ {
		id := fmt.Sprintf("m%02d", m)
		if evs, _ := s.get(id); len(evs) != perMatch {
			t.Errorf("%s: esperaba %d eventos, llegaron %d", id, perMatch, len(evs))
		}
	}
	st := p.Stats()
	if st.BatchesSent != matches || st.OpenMatches != 0 || r.all() != "" {
		t.Errorf("stats=%+v rechazos=%q", st, r.all())
	}
}

// El ejemplo de Jean, mandado desordenado, llega completo como UNA partida.
func TestSampleFileArrivesAsOneMatch(t *testing.T) {
	raw, err := os.ReadFile("../../../samples/combat_match.json")
	if err != nil {
		t.Fatal(err)
	}
	var batch struct{ Events []json.RawMessage }
	if err := json.Unmarshal(raw, &batch); err != nil {
		t.Fatal(err)
	}
	var evs []event.Event
	for _, r := range batch.Events {
		e, problems := event.Parse(r)
		if len(problems) > 0 {
			t.Fatal(problems)
		}
		evs = append(evs, e)
	}
	// Mandamos MATCH_FINISHED al final pero el resto en orden inverso.
	last := evs[len(evs)-1]
	rest := evs[:len(evs)-1]
	sort.Slice(rest, func(i, j int) bool { return rest[i].EventID > rest[j].EventID })

	p, s, _ := newTest(Config{})
	for _, e := range rest {
		p.Submit(e)
	}
	p.Submit(last)
	p.Close()

	if got, _ := s.get(last.MatchID); len(got) != len(evs) {
		t.Errorf("esperaba %d eventos, llegaron %d", len(evs), len(got))
	}
}
