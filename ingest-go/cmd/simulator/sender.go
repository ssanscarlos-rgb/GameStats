package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// sender manda eventos al servicio de ingestión por HTTP y cuenta los resultados.
// Lo usan muchas goroutines a la vez, por eso los contadores son atómicos.
type sender struct {
	url  string
	http *http.Client

	sent, accepted, rejected, failed atomic.Int64
}

func newSender(url string) *sender {
	return &sender{url: url, http: &http.Client{Timeout: 10 * time.Second}}
}

// sendEvent manda un evento bien formado.
func (s *sender) sendEvent(e Event) {
	body, _ := json.Marshal(e)
	s.sendRaw(body)
}

// sendRaw manda bytes tal cual. Sirve también para mandar JSON roto a propósito.
func (s *sender) sendRaw(body []byte) {
	s.sent.Add(1)
	resp, err := s.http.Post(s.url+"/events", "application/json", bytes.NewReader(body))
	if err != nil {
		s.failed.Add(1)
		return
	}
	defer resp.Body.Close()

	var r struct{ Accepted, Rejected int64 }
	if resp.StatusCode != http.StatusAccepted || json.NewDecoder(resp.Body).Decode(&r) != nil {
		io.Copy(io.Discard, resp.Body)
		s.rejected.Add(1) // ej. 400 por JSON ilegible: se rechazó la petición completa
		return
	}
	s.accepted.Add(r.Accepted)
	s.rejected.Add(r.Rejected)
}

// waitReady espera a que el servicio de ingestión responda en /health.
func (s *sender) waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if resp, err := s.http.Get(s.url + "/health"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("el servicio de ingestión no respondió en %s (%s/health)", timeout, s.url)
}
