// Simulador de GameStats: finge ser los juegos externos.
//
// Genera partidas de tres juegos (combat, chase, racing) y las "juega" en
// paralelo, una goroutine por partida, mandando cada evento por HTTP al
// servicio de ingestión (POST /events). Al final manda casos especiales:
// eventos inválidos, duplicados, un evento tardío y uno de otro juego.
//
//	go run ./cmd/simulator                       # contra localhost:8080
//	go run ./cmd/simulator -combat 5 -delay 0    # más partidas, sin pausas
//	go run ./cmd/simulator -out ../samples/sim   # escribir JSON en vez de enviar
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// planned es una partida ya generada, lista para enviarse.
type planned struct {
	id     string
	game   string
	note   string
	events []Event
}

func main() {
	url := flag.String("url", envOr("INGEST_URL", "http://localhost:8080"), "URL del servicio de ingestión")
	nCombat := flag.Int("combat", 3, "partidas de combat al azar")
	nChase := flag.Int("chase", 3, "partidas de chase (La Buena vs La Mala) al azar")
	nRacing := flag.Int("racing", 2, "carreras al azar")
	delay := flag.Duration("delay", 30*time.Millisecond, "pausa promedio entre eventos de una misma partida")
	seed := flag.Uint64("seed", 0, "semilla del azar (0 = distinta en cada ejecución)")
	special := flag.Bool("special", true, "incluir partidas guionizadas y casos especiales")
	out := flag.String("out", "", "carpeta donde escribir las partidas como JSON en lugar de enviarlas")
	flag.Parse()

	if *seed == 0 {
		*seed = uint64(time.Now().UnixNano())
	}
	run := fmt.Sprintf("%04x", *seed%0x10000) // va en los matchId: dos ejecuciones no chocan
	matches := plan(*seed, run, *nCombat, *nChase, *nRacing, *special)

	if *out != "" {
		if err := writeFiles(*out, matches); err != nil {
			log.Fatal(err)
		}
		return
	}

	s := newSender(*url)
	log.Printf("esperando al servicio de ingestión en %s ...", *url)
	if err := s.waitReady(60 * time.Second); err != nil {
		log.Fatal(err)
	}
	printPlan(matches, *seed)

	// Cada partida se juega en su propia goroutine: sus eventos se intercalan
	// con los de las demás, como si varios juegos corrieran al mismo tiempo.
	started := time.Now()
	var wg sync.WaitGroup
	for i, pm := range matches {
		wg.Add(1)
		go func(i int, pm planned) {
			defer wg.Done()
			r := rand.New(rand.NewPCG(*seed, uint64(1000+i)))
			for _, e := range pm.events {
				s.sendEvent(e)
				pause(r, *delay)
			}
		}(i, pm)
	}
	wg.Wait()

	if *special {
		sendSpecialCases(s, matches, run)
	}

	fmt.Printf("\nListo en %s. Peticiones: %d · eventos aceptados: %d · rechazados: %d · errores de red: %d\n",
		time.Since(started).Round(time.Millisecond), s.sent.Load(), s.accepted.Load(), s.rejected.Load(), s.failed.Load())
	fmt.Printf("Revisa: curl %s/status   y   curl %s/rejected\n", *url, *url)
}

// plan genera TODAS las partidas antes de enviar nada. Con la misma semilla
// se generan exactamente las mismas partidas (útil para repetir una demo).
func plan(seed uint64, run string, nCombat, nChase, nRacing int, special bool) []planned {
	base := time.Now().UTC().Truncate(time.Second)
	var ms []planned
	add := func(game, suffix, note string, gen func(r *rand.Rand, id string, start time.Time) []Event) {
		i := len(ms)
		id := fmt.Sprintf("%s-%s-%s", game, run, suffix)
		r := rand.New(rand.NewPCG(seed, uint64(i+1)))
		ms = append(ms, planned{id, game, note, gen(r, id, base.Add(time.Duration(i)*2*time.Second))})
	}

	for i := 1; i <= nCombat; i++ {
		add("combat", fmt.Sprintf("%02d", i), "al azar", simulateCombat)
	}
	for i := 1; i <= nChase; i++ {
		add("chase", fmt.Sprintf("%02d", i), "al azar", func(r *rand.Rand, id string, t time.Time) []Event {
			return simulateChase(r, id, t, chaseNormal)
		})
	}
	for i := 1; i <= nRacing; i++ {
		add("racing", fmt.Sprintf("%02d", i), "al azar", func(r *rand.Rand, id string, t time.Time) []Event {
			return simulateRace(r, id, t, 0)
		})
	}
	if special {
		add("combat", "remontada", "racha de bob + remontada de alice", func(_ *rand.Rand, id string, t time.Time) []Event {
			return simulateCombatComeback(id, t)
		})
		add("chase", "perfecta", "sin errores: pasos = ruta óptima", func(r *rand.Rand, id string, t time.Time) []Event {
			return simulateChase(r, id, t, chasePerfect)
		})
		add("chase", "sinruta", "La Mala encerrada: no hay ruta", func(r *rand.Rand, id string, t time.Time) []Event {
			return simulateChase(r, id, t, chaseNoRoute)
		})
		add("racing", "remontada", "vale choca en la vuelta 1 y gana", simulateRaceComeback)
		add("racing", "abandonada", "se corta en la vuelta 2: sin MATCH_FINISHED", func(r *rand.Rand, id string, t time.Time) []Event {
			return simulateRace(r, id, t, 2)
		})
	}
	return ms
}

// sendSpecialCases manda, después de las partidas, casos que el servicio debe
// detectar. Los inválidos se rechazan al recibirlos; duplicados, tardíos y
// juego equivocado se aceptan por HTTP y los descarta el pipeline (ver /status).
func sendSpecialCases(s *sender, matches []planned, run string) {
	fmt.Println("\nCasos especiales:")

	invalid := map[string]string{
		"timestamp sin Z":   `{"eventId":"bad-1","timestamp":"2026-10-14T10:00:00-06:00","gameId":"combat","matchId":"x","type":"SHOT_FIRED"}`,
		"sin matchId":       `{"eventId":"bad-2","timestamp":"2026-10-14T10:00:00Z","gameId":"combat","type":"SHOT_FIRED"}`,
		"gameId mayúscula":  `{"eventId":"bad-3","timestamp":"2026-10-14T10:00:00Z","gameId":"Combat","matchId":"x","type":"SHOT_FIRED"}`,
		"type minúscula":    `{"eventId":"bad-4","timestamp":"2026-10-14T10:00:00Z","gameId":"racing","matchId":"x","type":"lap_completed"}`,
		"data no es objeto": `{"eventId":"bad-5","timestamp":"2026-10-14T10:00:00Z","gameId":"chase","matchId":"x","type":"PLAYER_MOVED","data":42}`,
		"JSON roto":         `{"eventId":"bad-6", "timestamp": `,
	}
	for name, raw := range invalid {
		s.sendRaw([]byte(raw))
		fmt.Printf("  - inválido (%s)\n", name)
	}

	if first := firstFinished(matches); first != nil {
		for _, e := range first.events[:3] {
			s.sendEvent(e)
		}
		fmt.Printf("  - 3 duplicados de %s\n", first.id)

		late := first.events[len(first.events)-1]
		late.EventID, late.Type, late.PlayerID, late.Data = first.id+"-tarde", "PLAYER_LEFT", "intruso", nil
		s.sendEvent(late)
		fmt.Printf("  - 1 evento tardío para %s (ya terminada)\n", first.id)
	}

	// Una partida de combat que recibe un evento que dice ser de racing.
	m := newMatch("combat", "combat-"+run+"-mixta", time.Now().UTC())
	m.add("", "MATCH_STARTED", nil)
	wrong := Event{EventID: m.matchID + "-otro-juego", Timestamp: m.events[0].Timestamp, GameID: "racing",
		GameVersion: "1.0", MatchID: m.matchID, PlayerID: "luis", Type: "LAP_COMPLETED",
		Data: map[string]any{"lap": 1, "lapTimeMs": 61000}}
	s.sendEvent(m.events[0])
	s.sendEvent(wrong)
	for _, e := range m.finish("") {
		if e.Type == "MATCH_FINISHED" {
			s.sendEvent(e)
		}
	}
	fmt.Printf("  - 1 evento de racing dentro de %s (juego equivocado)\n", m.matchID)
}

func firstFinished(matches []planned) *planned {
	for i := range matches {
		if ev := matches[i].events; len(ev) > 3 && ev[len(ev)-1].Type == "MATCH_FINISHED" {
			return &matches[i]
		}
	}
	return nil
}

func printPlan(matches []planned, seed uint64) {
	fmt.Printf("Simulando %d partidas en paralelo (semilla %d):\n", len(matches), seed)
	for _, pm := range matches {
		fmt.Printf("  %-26s %-7s %4d eventos  %s\n", pm.id, pm.game, len(pm.events), pm.note)
	}
}

func writeFiles(dir string, matches []planned) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, pm := range matches {
		body, _ := json.MarshalIndent(map[string]any{"events": pm.events}, "", "  ")
		path := filepath.Join(dir, pm.id+".json")
		if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
			return err
		}
		fmt.Printf("%s (%d eventos)\n", path, len(pm.events))
	}
	return nil
}

// pause espera alrededor de d (entre 0.5d y 1.5d) para que las partidas se intercalen.
func pause(r *rand.Rand, d time.Duration) {
	if d > 0 {
		time.Sleep(d/2 + time.Duration(r.Int64N(int64(d)+1)))
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
