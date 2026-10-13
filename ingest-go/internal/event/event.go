// Package event define qué es un evento de GameStats y cómo se valida.
// Es la "puerta de control de calidad": nada pasa a Scala sin pasar por aquí.
package event

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Event es un hecho ocurrido en una partida. Los nombres JSON siguen docs/CONTRACT.md.
//
// Los campos opcionales son punteros (*string): así distinguimos "no vino" (nil)
// de "vino vacío" (""). Data es json.RawMessage: Go guarda el JSON tal cual sin
// entenderlo, por eso sirve para cualquier juego (combat, racing, ...).
type Event struct {
	EventID     string          `json:"eventId"`
	Timestamp   string          `json:"timestamp"`
	GameID      string          `json:"gameId"`
	GameVersion *string         `json:"gameVersion,omitempty"`
	MatchID     string          `json:"matchId"`
	PlayerID    *string         `json:"playerId,omitempty"`
	Type        string          `json:"type"`
	Action      *string         `json:"action,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`

	// Time es el timestamp ya convertido a fecha. No se envía en el JSON (`json:"-"`);
	// lo usamos dentro de Go, por ejemplo para saber cuándo una partida lleva rato quieta.
	Time time.Time `json:"-"`
}

// Tipos de evento generales que todos los juegos comparten.
const (
	TypeMatchStarted  = "MATCH_STARTED"
	TypeMatchFinished = "MATCH_FINISHED"
)

var (
	gameIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`) // minúsculas: "combat", "racing"
	typePattern   = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)  // MAYUSCULAS_CON_GUION_BAJO
)

// Parse convierte el JSON crudo de UN evento en un Event y lo valida.
// Devuelve todos los problemas encontrados (no solo el primero), para que el
// registro de rechazados diga exactamente qué estaba mal.
func Parse(raw json.RawMessage) (Event, []string) {
	var e Event
	if err := json.Unmarshal(raw, &e); err != nil {
		return e, []string{"JSON inválido o con tipos incorrectos: " + err.Error()}
	}
	problems := e.Validate()
	if len(problems) == 0 {
		e.Time, _ = time.Parse(time.RFC3339Nano, e.Timestamp) // ya validado
	}
	return e, problems
}

// Validate revisa las reglas del contrato. Lista vacía = evento válido.
//
// Tiene que ser estricta porque Scala rechaza el lote COMPLETO si un solo
// evento viene mal: un timestamp roto haría perder una partida entera.
func (e Event) Validate() []string {
	var p []string

	if strings.TrimSpace(e.EventID) == "" {
		p = append(p, "eventId es obligatorio")
	}

	switch {
	case e.Timestamp == "":
		p = append(p, "timestamp es obligatorio")
	case !strings.HasSuffix(e.Timestamp, "Z"):
		p = append(p, fmt.Sprintf("timestamp %q debe estar en UTC y terminar en Z", e.Timestamp))
	default:
		if _, err := time.Parse(time.RFC3339Nano, e.Timestamp); err != nil {
			p = append(p, fmt.Sprintf("timestamp %q no es una fecha ISO-8601 válida", e.Timestamp))
		}
	}

	switch {
	case e.GameID == "":
		p = append(p, "gameId es obligatorio")
	case !gameIDPattern.MatchString(e.GameID):
		p = append(p, fmt.Sprintf("gameId %q debe ir en minúsculas (ej. combat)", e.GameID))
	}

	if strings.TrimSpace(e.MatchID) == "" {
		p = append(p, "matchId es obligatorio")
	}

	switch {
	case e.Type == "":
		p = append(p, "type es obligatorio")
	case !typePattern.MatchString(e.Type):
		p = append(p, fmt.Sprintf("type %q debe ir en MAYUSCULAS_CON_GUION_BAJO", e.Type))
	}

	if e.PlayerID != nil && strings.TrimSpace(*e.PlayerID) == "" {
		p = append(p, "playerId, si viene, no puede estar vacío")
	}

	// data es opcional, pero si viene tiene que ser un objeto {...}, no un número o texto.
	if d := bytes.TrimSpace(e.Data); len(d) > 0 && !bytes.Equal(d, []byte("null")) && d[0] != '{' {
		p = append(p, "data, si viene, debe ser un objeto JSON")
	}

	return p
}
