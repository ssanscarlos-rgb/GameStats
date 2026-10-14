// Package analyzer es el cliente HTTP que entrega partidas al módulo Scala
// (POST /analyze, ver docs/CONTRACT.md).
package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ssanscarlos-rgb/gamestats/ingest-go/internal/event"
)

type Client struct {
	BaseURL     string        // ej. http://analyzer:8081
	HTTP        *http.Client  // cliente HTTP con timeout
	MaxAttempts int           // intentos antes de rendirse
	Backoff     time.Duration // espera antes del 2º intento; luego se duplica
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL:     strings.TrimRight(baseURL, "/"),
		HTTP:        &http.Client{Timeout: 30 * time.Second},
		MaxAttempts: 6,
		Backoff:     time.Second,
	}
}

// PermanentError significa "no tiene sentido reintentar": Scala dijo que la
// petición está mal (4xx). Reintentarla daría el mismo error.
type PermanentError struct {
	Status int
	Body   string
}

func (e *PermanentError) Error() string {
	return fmt.Sprintf("Scala rechazó la petición (%d): %s", e.Status, e.Body)
}

// Send envía una partida completa. Si Scala no responde o da error 5xx,
// reintenta esperando 1s, 2s, 4s, 8s... (backoff exponencial). Esto también
// cubre el caso en que Scala todavía está arrancando al iniciar Docker Compose.
func (c *Client) Send(ctx context.Context, matchID string, events []event.Event) error {
	body, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		return err
	}

	for attempt := 1; ; attempt++ {
		err := c.post(ctx, body)
		if err == nil {
			return nil
		}
		var perm *PermanentError
		if errors.As(err, &perm) || attempt >= c.MaxAttempts {
			return fmt.Errorf("partida %s, intento %d: %w", matchID, attempt, err)
		}

		wait := c.Backoff << (attempt - 1) // << duplica: 1s, 2s, 4s...
		log.Printf("partida %s: intento %d falló (%v); reintento en %s", matchID, attempt, err, wait)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Client) post(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/analyze", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err // Scala apagado, red caída, etc. → se reintenta
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return &PermanentError{Status: resp.StatusCode, Body: string(msg)}
	default:
		return fmt.Errorf("Scala respondió %d: %s", resp.StatusCode, msg)
	}
}
