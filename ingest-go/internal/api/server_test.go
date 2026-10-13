package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/ssanscarlos-rgb/gamestats/ingest-go/internal/event"
)

// newTestServer arma un servidor de prueba que guarda en una lista los eventos aceptados.
func newTestServer() (*Server, *[]event.Event) {
	var mu sync.Mutex
	var got []event.Event
	sink := func(e event.Event) { mu.Lock(); got = append(got, e); mu.Unlock() }
	return NewServer(sink, NewRejectedLog(100)), &got
}

func post(t *testing.T, s *Server, body string) (int, postResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(body))
	rec := httptest.NewRecorder() // un "navegador falso" que guarda la respuesta
	s.Routes().ServeHTTP(rec, req)
	var resp postResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

const okEvent = `{"eventId":"a1","timestamp":"2026-09-01T10:00:00Z","gameId":"combat","matchId":"m1","type":"MATCH_STARTED"}`
const badEvent = `{"eventId":"a2","timestamp":"ayer","gameId":"combat","matchId":"m1","type":"SHOT_FIRED"}`

func TestAcceptsSampleFile(t *testing.T) {
	body, err := os.ReadFile("../../../samples/combat_match.json")
	if err != nil {
		t.Fatal(err)
	}
	s, got := newTestServer()
	code, resp := post(t, s, string(body))
	if code != http.StatusAccepted || resp.Rejected != 0 || len(*got) != resp.Accepted || resp.Accepted == 0 {
		t.Fatalf("code=%d resp=%+v aceptados=%d", code, resp, len(*got))
	}
}

func TestThreeBodyShapes(t *testing.T) {
	for name, body := range map[string]string{
		"envoltorio": `{"events":[` + okEvent + `]}`,
		"arreglo":    `[` + okEvent + `]`,
		"uno solo":   okEvent,
	} {
		t.Run(name, func(t *testing.T) {
			s, got := newTestServer()
			if code, resp := post(t, s, body); code != http.StatusAccepted || resp.Accepted != 1 || len(*got) != 1 {
				t.Fatalf("code=%d resp=%+v", code, resp)
			}
		})
	}
}

// Un evento malo NO debe impedir que pasen los buenos del mismo lote.
func TestBadEventDoesNotBlockGoodOnes(t *testing.T) {
	s, got := newTestServer()
	_, resp := post(t, s, `[`+okEvent+`,`+badEvent+`]`)
	if resp.Accepted != 1 || resp.Rejected != 1 || len(*got) != 1 {
		t.Fatalf("resp=%+v", resp)
	}
	if resp.Errors[0].Index != 1 || resp.Errors[0].EventID != "a2" {
		t.Errorf("el error debería señalar el evento 1 (a2): %+v", resp.Errors[0])
	}

	items, total := s.rejected.Snapshot()
	if total != 1 || items[0].EventID != "a2" {
		t.Errorf("el rechazado debería quedar registrado: total=%d items=%+v", total, items)
	}
}

func TestGarbageBodyIs400(t *testing.T) {
	s, _ := newTestServer()
	for _, body := range []string{"", "hola", `{"events":[`} {
		if code, _ := post(t, s, body); code != http.StatusBadRequest {
			t.Errorf("cuerpo %q: esperaba 400, obtuve %d", body, code)
		}
	}
}

func TestRejectedLogKeepsOnlyLast(t *testing.T) {
	l := NewRejectedLog(2)
	for _, id := range []string{"1", "2", "3"} {
		l.Add(Rejected{EventID: id})
	}
	items, total := l.Snapshot()
	if total != 3 || len(items) != 2 || items[0].EventID != "2" {
		t.Errorf("total=%d items=%+v", total, items)
	}
}
