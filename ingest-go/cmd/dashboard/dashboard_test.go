package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestArgs(t *testing.T) {
	ok := []simRequest{
		{Mode: "batch", Combat: 3, Chase: 3, Racing: 2, Special: true, DelayMs: 30},
		{Mode: "batch", Special: true}, // solo casos especiales
		{Mode: "continuous", DurationSec: 120, Parallel: 6, DelayMs: 30, Seed: 42},
	}
	for _, q := range ok {
		if _, err := q.args("http://ingest:8080"); err != nil {
			t.Errorf("%+v debería ser válida: %v", q, err)
		}
	}

	bad := map[string]simRequest{
		"modo desconocido":   {Mode: "turbo"},
		"sin partidas":       {Mode: "batch"},
		"demasiadas":         {Mode: "batch", Combat: 500},
		"negativas":          {Mode: "batch", Racing: -1, Special: true},
		"pausa enorme":       {Mode: "batch", Combat: 1, DelayMs: 60000},
		"duración muy corta": {Mode: "continuous", DurationSec: 2, Parallel: 4},
		"sin paralelas":      {Mode: "continuous", DurationSec: 60},
	}
	for name, q := range bad {
		if _, err := q.args("http://ingest:8080"); err == nil {
			t.Errorf("%s: debería rechazarse", name)
		}
	}

	args, _ := simRequest{Mode: "continuous", DurationSec: 90, Parallel: 4, Seed: 7}.args("http://x")
	for _, want := range []string{"-duration", "90s", "-parallel", "4", "-seed", "7", "-url", "http://x"} {
		if !slices.Contains(args, want) {
			t.Errorf("faltan flags: %v no tiene %q", args, want)
		}
	}
}

// La página solo puede LEER de ingest y Scala a través del panel.
func TestProxyOnlyForwardsGET(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	h := routes(upstream.URL, upstream.URL, newSimRunner(upstream.URL, ""))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/analyzer/results/players", nil))
	if rec.Code != http.StatusOK || gotPath != "/results/players" {
		t.Fatalf("GET: code=%d path=%q", rec.Code, gotPath)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/ingest/events", strings.NewReader(`{}`)))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST a ingest debería bloquearse, dio %d", rec.Code)
	}
}

func TestProxyServiceDownIs502(t *testing.T) {
	h := routes("http://127.0.0.1:1", "http://127.0.0.1:1", newSimRunner("", ""))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ingest/status", nil))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("esperaba 502, obtuve %d", rec.Code)
	}
}

func TestPageIsServed(t *testing.T) {
	h := routes("http://x", "http://x", newSimRunner("", ""))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "GameStats") {
		t.Errorf("la página no se sirvió: %d", rec.Code)
	}
}

// fakeSimulator escribe un script de shell que hace de simulador.
func fakeSimulator(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("usa un script de shell")
	}
	p := filepath.Join(t.TempDir(), "simulator")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitStatus(t *testing.T, r *simRunner, want string) simState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s, _, _ := r.Snapshot(0); s.Status == want {
			return s
		}
		time.Sleep(20 * time.Millisecond)
	}
	s, _, _ := r.Snapshot(0)
	t.Fatalf("esperaba estado %q, quedó %q", want, s.Status)
	return s
}

func TestRunnerCapturesOutput(t *testing.T) {
	bin := fakeSimulator(t, `echo "2026/10/15 03:00:00 esperando al servicio"; echo "Listo"`)
	r := newSimRunner("http://ingest:8080", bin)

	if err := r.Start(simRequest{Mode: "batch", Combat: 1}); err != nil {
		t.Fatal(err)
	}
	s := waitStatus(t, r, "finished")
	if s.ExitCode == nil || *s.ExitCode != 0 || s.Run != 1 {
		t.Errorf("estado final raro: %+v", s)
	}

	_, lines, last := r.Snapshot(0)
	var texts []string
	for _, l := range lines {
		texts = append(texts, l.Text)
	}
	if !slices.Contains(texts, "esperando al servicio") || !slices.Contains(texts, "Listo") {
		t.Errorf("faltan líneas (o no se quitó la fecha): %q", texts)
	}
	if _, newer, _ := r.Snapshot(last); len(newer) != 0 {
		t.Errorf("since=%d no debería devolver nada, devolvió %d", last, len(newer))
	}
}

func TestRunnerStopAndBusy(t *testing.T) {
	bin := fakeSimulator(t, `trap 'echo adios; exit 0' INT; while true; do sleep 0.05; done`)
	r := newSimRunner("http://ingest:8080", bin)

	if err := r.Start(simRequest{Mode: "continuous", DurationSec: 60, Parallel: 2}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, r, "running")

	if err := r.Start(simRequest{Mode: "batch", Combat: 1}); !errors.Is(err, errBusy) {
		t.Errorf("un segundo inicio debería dar errBusy, dio %v", err)
	}

	r.Stop()
	waitStatus(t, r, "stopped")
}

func TestRunnerMissingBinaryFails(t *testing.T) {
	r := newSimRunner("http://ingest:8080", filepath.Join(t.TempDir(), "no-existe"))
	if err := r.Start(simRequest{Mode: "batch", Combat: 1}); err != nil {
		t.Fatal(err)
	}
	s := waitStatus(t, r, "failed")
	if !strings.Contains(s.Error, "no existe") {
		t.Errorf("el error debería explicar qué falta: %q", s.Error)
	}
}
