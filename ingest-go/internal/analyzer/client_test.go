package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ssanscarlos-rgb/gamestats/ingest-go/internal/event"
)

var sample = []event.Event{{EventID: "1", Timestamp: "2026-09-01T10:00:00Z", GameID: "combat", MatchID: "m1", Type: "MATCH_STARTED"}}

// fakeScala responde con los códigos indicados, uno por llamada.
func fakeScala(t *testing.T, codes ...int) (*httptest.Server, *atomic.Int32) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.URL.Path != "/analyze" || r.Method != http.MethodPost {
			t.Errorf("petición inesperada %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var req struct{ Events []map[string]any }
		if err := json.Unmarshal(body, &req); err != nil || len(req.Events) != 1 {
			t.Errorf("cuerpo inesperado: %s", body)
		}
		w.WriteHeader(codes[min(int(n), len(codes))-1])
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func fastClient(url string) *Client {
	c := New(url)
	c.Backoff = time.Millisecond
	c.MaxAttempts = 3
	return c
}

func TestSendOK(t *testing.T) {
	srv, calls := fakeScala(t, 200)
	if err := fastClient(srv.URL).Send(context.Background(), "m1", sample); err != nil || calls.Load() != 1 {
		t.Fatalf("err=%v llamadas=%d", err, calls.Load())
	}
}

func TestRetriesOnServerError(t *testing.T) {
	srv, calls := fakeScala(t, 500, 503, 200)
	if err := fastClient(srv.URL).Send(context.Background(), "m1", sample); err != nil || calls.Load() != 3 {
		t.Fatalf("err=%v llamadas=%d", err, calls.Load())
	}
}

func TestNoRetryOnBadRequest(t *testing.T) {
	srv, calls := fakeScala(t, 400)
	err := fastClient(srv.URL).Send(context.Background(), "m1", sample)
	var perm *PermanentError
	if !errors.As(err, &perm) || calls.Load() != 1 {
		t.Fatalf("esperaba error permanente sin reintentos: err=%v llamadas=%d", err, calls.Load())
	}
}

func TestGivesUpAfterMaxAttempts(t *testing.T) {
	srv, calls := fakeScala(t, 500)
	if err := fastClient(srv.URL).Send(context.Background(), "m1", sample); err == nil || calls.Load() != 3 {
		t.Fatalf("err=%v llamadas=%d", err, calls.Load())
	}
}
