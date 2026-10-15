package main

import (
	"encoding/json"
	"math/rand/v2"
	"testing"
	"time"

	// Solo las PRUEBAS usan el validador real del servicio: así se garantiza
	// que todo lo que genera el simulador cumple el contrato. El simulador en
	// sí no depende de ningún paquete interno.
	"github.com/ssanscarlos-rgb/gamestats/ingest-go/internal/event"
)

var t0 = time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

// checkMatch revisa las reglas que toda partida generada debe cumplir.
func checkMatch(t *testing.T, name string, evs []Event, wantFinished bool) {
	t.Helper()
	if len(evs) < 3 {
		t.Fatalf("%s: muy pocos eventos (%d)", name, len(evs))
	}
	if evs[0].Type != "MATCH_STARTED" {
		t.Errorf("%s: el primer evento es %s", name, evs[0].Type)
	}
	if finished := evs[len(evs)-1].Type == "MATCH_FINISHED"; finished != wantFinished {
		t.Errorf("%s: terminada=%v, se esperaba %v", name, finished, wantFinished)
	}
	ids := map[string]bool{}
	last := ""
	for i, e := range evs {
		raw, _ := json.Marshal(e)
		if _, problems := event.Parse(raw); len(problems) > 0 {
			t.Errorf("%s: evento %d inválido: %v", name, i, problems)
		}
		if ids[e.EventID] {
			t.Errorf("%s: eventId repetido %s", name, e.EventID)
		}
		ids[e.EventID] = true
		if e.Timestamp < last { // mismo formato fijo: orden de texto = orden de tiempo
			t.Errorf("%s: el tiempo retrocede en el evento %d", name, i)
		}
		last = e.Timestamp
	}
}

func count(evs []Event, typ string) int {
	n := 0
	for _, e := range evs {
		if e.Type == typ {
			n++
		}
	}
	return n
}

func winner(evs []Event) string {
	if w, ok := evs[len(evs)-1].Data["winnerId"].(string); ok {
		return w
	}
	return ""
}

func TestGeneratedMatchesAreValid(t *testing.T) {
	for seed := uint64(1); seed <= 30; seed++ {
		r := func() *rand.Rand { return rand.New(rand.NewPCG(seed, 7)) }
		checkMatch(t, "combat", simulateCombat(r(), "combat-x", t0), true)
		checkMatch(t, "chase", simulateChase(r(), "chase-x", t0, chaseNormal), true)
		checkMatch(t, "racing", simulateRace(r(), "racing-x", t0, 0), true)
		checkMatch(t, "racing abandonada", simulateRace(r(), "racing-y", t0, 2), false)
	}
}

func TestCombatWinnerHasFiveKills(t *testing.T) {
	for seed := uint64(1); seed <= 30; seed++ {
		evs := simulateCombat(rand.New(rand.NewPCG(seed, 1)), "c", t0)
		kills := 0
		for _, e := range evs {
			if e.Type == "PLAYER_ELIMINATED" && e.PlayerID == winner(evs) {
				kills++
			}
		}
		if kills != killsToWin {
			t.Errorf("semilla %d: el ganador %q tiene %d eliminaciones", seed, winner(evs), kills)
		}
	}
}

func TestCombatComebackScript(t *testing.T) {
	evs := simulateCombatComeback("c", t0)
	checkMatch(t, "remontada", evs, true)
	if winner(evs) != "alice" || count(evs, "PLAYER_ELIMINATED") != 7 {
		t.Errorf("ganador=%s eliminaciones=%d", winner(evs), count(evs, "PLAYER_ELIMINATED"))
	}
}

func TestChaseRules(t *testing.T) {
	for seed := uint64(1); seed <= 50; seed++ {
		evs := simulateChase(rand.New(rand.NewPCG(seed, 3)), "ch", t0, chaseNormal)
		moves, hits := count(evs, "PLAYER_MOVED"), count(evs, "OBSTACLE_HIT")
		if hits > 3 {
			t.Errorf("semilla %d: %d choques con solo 3 vidas", seed, hits)
		}
		reached := count(evs, "TARGET_REACHED") == 1
		if reached != (winner(evs) != "") {
			t.Errorf("semilla %d: alcanzó=%v pero ganador=%q", seed, reached, winner(evs))
		}
		for _, e := range evs {
			if e.Type == "TARGET_REACHED" && e.Data["steps"] != moves {
				t.Errorf("semilla %d: steps=%v pero hubo %d movimientos", seed, e.Data["steps"], moves)
			}
		}
	}
}

func TestChasePerfectRunUsesOptimalSteps(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		evs := simulateChase(rand.New(rand.NewPCG(seed, 5)), "ch", t0, chasePerfect)
		var optimal, steps any
		for _, e := range evs {
			switch e.Type {
			case "ROUTE_COMPUTED":
				optimal = e.Data["optimalSteps"]
			case "TARGET_REACHED":
				steps = e.Data["steps"]
			}
		}
		if optimal == nil || optimal != steps || count(evs, "OBSTACLE_HIT") != 0 {
			t.Errorf("semilla %d: óptimo=%v pasos=%v", seed, optimal, steps)
		}
	}
}

func TestChaseNoRoute(t *testing.T) {
	evs := simulateChase(rand.New(rand.NewPCG(9, 9)), "ch", t0, chaseNoRoute)
	checkMatch(t, "sin ruta", evs, true)
	if count(evs, "PLAYER_MOVED") != 0 || winner(evs) != "" {
		t.Errorf("sin ruta no debería haber movimientos ni ganador")
	}
}

func TestRaceComebackScript(t *testing.T) {
	evs := simulateRaceComeback(rand.New(rand.NewPCG(1, 1)), "r", t0)
	if winner(evs) != "vale" {
		t.Fatalf("ganó %s", winner(evs))
	}
	// vale debe ser la última en completar la vuelta 1.
	var lap1 []string
	for _, e := range evs {
		if e.Type == "LAP_COMPLETED" && e.Data["lap"] == 1 {
			lap1 = append(lap1, e.PlayerID)
		}
	}
	if lap1[len(lap1)-1] != "vale" {
		t.Errorf("orden en la vuelta 1: %v", lap1)
	}
}

func TestSameSeedSameMatches(t *testing.T) {
	a := plan(42, "abcd", 2, 2, 2, true)
	b := plan(42, "abcd", 2, 2, 2, true)
	for i := range a {
		if len(a[i].events) != len(b[i].events) || a[i].events[len(a[i].events)-1].Type != b[i].events[len(b[i].events)-1].Type {
			t.Errorf("%s cambió con la misma semilla", a[i].id)
		}
	}
}

func TestRandomMatchesAreValid(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 3))
	games := map[string]int{}
	for n := 1; n <= 60; n++ {
		pm := randomMatch(r, 3, "test", n)
		games[pm.game]++
		checkMatch(t, pm.id, pm.events, true)
	}
	if len(games) != 3 {
		t.Errorf("deberían salir los tres juegos: %v", games)
	}
}
