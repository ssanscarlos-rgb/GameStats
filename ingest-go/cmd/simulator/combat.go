package main

import (
	"math/rand/v2"
	"time"
)

// Juego "combat": todos contra todos. Gana quien llega primero a killsToWin
// eliminaciones. Quien es eliminado reaparece con la vida llena.
//
// Eventos (ver docs/CONTRACT.md, sección 2.2):
//   SHOT_FIRED        playerId = tirador   data {weapon}
//   DAMAGE_DEALT      playerId = atacante  data {target, amount, weapon}
//   PLAYER_ELIMINATED playerId = asesino   data {victimId, weapon}

var weaponDamage = map[string]int{"rifle": 34, "pistola": 25, "escopeta": 50}
var weapons = []string{"rifle", "pistola", "escopeta"}

const killsToWin = 5

func simulateCombat(r *rand.Rand, matchID string, start time.Time) []Event {
	m := newMatch("combat", matchID, start)
	players := pickPlayers(r, 2+r.IntN(3)) // de 2 a 4 jugadores

	// Cada jugador tiene su propia puntería: así las estadísticas difieren.
	accuracy := map[string]float64{}
	hp := map[string]int{}
	kills := map[string]int{}
	for _, p := range players {
		accuracy[p] = 0.35 + r.Float64()*0.5
		hp[p] = 100
	}

	m.add("", "MATCH_STARTED", nil)
	for _, p := range players {
		m.wait(between(r, 200*time.Millisecond, 1500*time.Millisecond))
		m.add(p, "PLAYER_JOINED", nil)
	}

	for shot := 0; shot < 600; shot++ { // tope de seguridad
		m.wait(between(r, time.Second, 5*time.Second))
		shooter := players[r.IntN(len(players))]
		target := shooter
		for target == shooter {
			target = players[r.IntN(len(players))]
		}
		w := weapons[r.IntN(len(weapons))]

		m.add(shooter, "SHOT_FIRED", map[string]any{"weapon": w})
		if r.Float64() >= accuracy[shooter] {
			continue // falló el disparo
		}
		dmg := weaponDamage[w] + r.IntN(11) - 5
		m.add(shooter, "DAMAGE_DEALT", map[string]any{"target": target, "amount": dmg, "weapon": w})

		hp[target] -= dmg
		if hp[target] > 0 {
			continue
		}
		m.wait(300 * time.Millisecond)
		m.add(shooter, "PLAYER_ELIMINATED", map[string]any{"victimId": target, "weapon": w})
		hp[target] = 100 // reaparece
		kills[shooter]++
		if kills[shooter] >= killsToWin {
			return m.finish(shooter)
		}
	}
	return m.finish(leader(kills))
}

// simulateCombatComeback es una partida guionizada para disparar las dos reglas
// de Scala: bob logra una racha (KILL_STREAK) y alice remonta y gana (COMEBACK).
func simulateCombatComeback(matchID string, start time.Time) []Event {
	m := newMatch("combat", matchID, start)
	m.add("", "MATCH_STARTED", nil)
	m.wait(time.Second)
	m.add("alice", "PLAYER_JOINED", nil)
	m.add("bob", "PLAYER_JOINED", nil)

	kill := func(shooter, victim string, gap time.Duration) {
		m.wait(gap)
		m.add(shooter, "SHOT_FIRED", map[string]any{"weapon": "rifle"})
		m.add(shooter, "DAMAGE_DEALT", map[string]any{"target": victim, "amount": 100, "weapon": "rifle"})
		m.wait(500 * time.Millisecond)
		m.add(shooter, "PLAYER_ELIMINATED", map[string]any{"victimId": victim, "weapon": "rifle"})
	}

	kill("bob", "alice", 8*time.Second) // racha de bob: 3 eliminaciones, <= 10 s entre cada una
	kill("bob", "alice", 5*time.Second)
	kill("bob", "alice", 6*time.Second)
	m.wait(15 * time.Second)
	m.add("alice", "SHOT_FIRED", map[string]any{"weapon": "pistola"}) // un fallo
	kill("alice", "bob", 20*time.Second)                              // remontada de alice: de 0-3 a 4-3
	kill("alice", "bob", 25*time.Second)
	kill("alice", "bob", 30*time.Second)
	kill("alice", "bob", 40*time.Second)
	m.wait(5 * time.Second)
	return m.finish("alice")
}

func leader(score map[string]int) string {
	best, bestN := "", -1
	for p, n := range score {
		if n > bestN || (n == bestN && p < best) {
			best, bestN = p, n
		}
	}
	return best
}
