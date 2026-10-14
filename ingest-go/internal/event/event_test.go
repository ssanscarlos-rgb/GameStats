package event

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Todos los eventos de los ejemplos compartidos con Scala (samples/*.json)
// tienen que pasar la validación.
func TestSamplesAreValid(t *testing.T) {
	files, _ := filepath.Glob("../../../samples/*.json")
	if len(files) == 0 {
		t.Fatal("no se encontraron ejemplos en samples/")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var batch struct {
			Events []json.RawMessage `json:"events"`
		}
		if err := json.Unmarshal(raw, &batch); err != nil || len(batch.Events) == 0 {
			t.Fatalf("%s: sin eventos (%v)", f, err)
		}
		for i, r := range batch.Events {
			if _, problems := Parse(r); len(problems) > 0 {
				t.Errorf("%s, evento %d rechazado: %v", f, i, problems)
			}
		}
	}
}

func TestParseValidEvent(t *testing.T) {
	e, problems := Parse(json.RawMessage(`{
		"eventId":"x-1","timestamp":"2026-09-01T10:00:20.350Z","gameId":"combat",
		"matchId":"m-1","playerId":"bob","type":"PLAYER_ELIMINATED",
		"data":{"victimId":"alice","weapon":"rifle"}}`))
	if len(problems) > 0 {
		t.Fatalf("no debería tener problemas: %v", problems)
	}
	if e.Time.IsZero() {
		t.Error("Time debería quedar calculado")
	}
}

// Cada caso rompe UNA regla y verificamos que el mensaje la mencione.
func TestInvalidEvents(t *testing.T) {
	const base = `"eventId":"x","timestamp":"2026-09-01T10:00:00Z","gameId":"combat","matchId":"m","type":"SHOT_FIRED"`
	cases := []struct {
		name, json, expect string
	}{
		{"sin eventId", `{"timestamp":"2026-09-01T10:00:00Z","gameId":"combat","matchId":"m","type":"A"}`, "eventId"},
		{"sin timestamp", `{"eventId":"x","gameId":"combat","matchId":"m","type":"A"}`, "timestamp es obligatorio"},
		{"timestamp sin Z", `{"eventId":"x","timestamp":"2026-09-01T10:00:00-06:00","gameId":"combat","matchId":"m","type":"A"}`, "UTC"},
		{"timestamp basura", `{"eventId":"x","timestamp":"ayerZ","gameId":"combat","matchId":"m","type":"A"}`, "no es una fecha"},
		{"gameId mayúsculas", `{"eventId":"x","timestamp":"2026-09-01T10:00:00Z","gameId":"Combat","matchId":"m","type":"A"}`, "minúsculas"},
		{"sin matchId", `{"eventId":"x","timestamp":"2026-09-01T10:00:00Z","gameId":"combat","type":"A"}`, "matchId"},
		{"type minúsculas", `{"eventId":"x","timestamp":"2026-09-01T10:00:00Z","gameId":"combat","matchId":"m","type":"shot"}`, "MAYUSCULAS"},
		{"playerId vacío", `{` + base + `,"playerId":""}`, "playerId"},
		{"data no es objeto", `{` + base + `,"data":42}`, "data"},
		{"tipo incorrecto", `{"eventId":123}`, "JSON inválido"},
		{"no es JSON", `esto no es json`, "JSON inválido"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, problems := Parse(json.RawMessage(c.json))
			if !strings.Contains(strings.Join(problems, " | "), c.expect) {
				t.Errorf("esperaba un problema con %q, obtuve %v", c.expect, problems)
			}
		})
	}
}

func TestDataNullIsAllowed(t *testing.T) {
	_, problems := Parse(json.RawMessage(`{"eventId":"x","timestamp":"2026-09-01T10:00:00Z","gameId":"combat","matchId":"m","type":"MATCH_STARTED","data":null}`))
	if len(problems) > 0 {
		t.Errorf("data null es válido según el contrato: %v", problems)
	}
}
