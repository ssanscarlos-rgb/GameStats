package main

import (
	"math/rand/v2"
	"sort"
	"time"
)

// Juego "racing": carrera de N vueltas. Pensado para ser lo más sencillo de
// implementar después como juego real: el juego solo tiene que avisar cuando
// alguien completa una vuelta (con su tiempo) y cuando alguien choca.
// Las posiciones, la mejor vuelta o las remontadas NO las manda el juego:
// Scala las calcula a partir de los tiempos de vuelta.
//
// Eventos (ver docs/CONTRACT.md, sección 2.3):
//   MATCH_STARTED   data {track, laps}
//   CRASH           playerId  data {lap, timeLostMs}
//   LAP_COMPLETED   playerId  data {lap, lapTimeMs}   (lapTimeMs ya incluye el tiempo perdido)
//   MATCH_FINISHED  data {winnerId} = quien tiene el menor tiempo total

var tracks = []string{"Circuito Arenal", "Pista Irazu", "Ovalo Poas", "Ruta Tenorio"}

// racer describe cómo maneja un piloto: su ritmo base y qué tan constante es.
type racer struct {
	name      string
	paceMs    int     // tiempo típico de vuelta
	spreadMs  int     // cuánto varía de vuelta a vuelta
	crashProb float64 // probabilidad de chocar en cada vuelta
}

// timed es un evento que todavía no tiene hora: se ordenan antes de emitirse.
type timed struct {
	at     time.Duration
	player string
	typ    string
	data   map[string]any
}

func simulateRace(r *rand.Rand, matchID string, start time.Time, abandonAfterLap int) []Event {
	names := pickPlayers(r, 3+r.IntN(4)) // de 3 a 6 pilotos
	racers := make([]racer, len(names))
	for i, n := range names {
		racers[i] = racer{n, 60000 + r.IntN(12000), 300 + r.IntN(2500), 0.04 + r.Float64()*0.08}
	}
	return runRace(r, matchID, start, tracks[r.IntN(len(tracks))], 3+r.IntN(3), racers, nil, abandonAfterLap)
}

// simulateRaceComeback es una carrera guionizada: "vale" choca en la vuelta 1,
// queda última, y gana igual por ser la más rápida. Sirve para que Scala
// detecte una remontada (ganadora que estuvo fuera del top 3).
func simulateRaceComeback(r *rand.Rand, matchID string, start time.Time) []Event {
	racers := []racer{
		{"vale", 58000, 300, 0},
		{"luis", 63000, 300, 0},
		{"sofi", 64000, 300, 0},
		{"leo", 65500, 300, 0},
	}
	forced := map[string]int{"vale": 12000} // vale pierde 12 s en la vuelta 1
	return runRace(r, matchID, start, "Circuito Arenal", 4, racers, forced, 0)
}

func runRace(r *rand.Rand, matchID string, start time.Time, track string, laps int,
	racers []racer, forcedCrashLap1 map[string]int, abandonAfterLap int) []Event {

	var pending []timed
	totals := map[string]int{}

	for _, rc := range racers {
		elapsed := 0
		for lap := 1; lap <= laps; lap++ {
			if abandonAfterLap > 0 && lap > abandonAfterLap {
				break // la carrera se corta: el juego "se cae"
			}
			lapMs := rc.paceMs + int(r.NormFloat64()*float64(rc.spreadMs))
			lost := 0
			if lap == 1 && forcedCrashLap1[rc.name] > 0 {
				lost = forcedCrashLap1[rc.name]
			} else if r.Float64() < rc.crashProb {
				lost = 3000 + r.IntN(6000)
			}
			if lost > 0 {
				pending = append(pending, timed{
					at: ms(elapsed + lapMs/2), player: rc.name, typ: "CRASH",
					data: map[string]any{"lap": lap, "timeLostMs": lost},
				})
				lapMs += lost
			}
			elapsed += lapMs
			pending = append(pending, timed{
				at: ms(elapsed), player: rc.name, typ: "LAP_COMPLETED",
				data: map[string]any{"lap": lap, "lapTimeMs": lapMs},
			})
		}
		totals[rc.name] = elapsed
	}

	// Los eventos de todos los pilotos se intercalan en el orden en que ocurren.
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].at < pending[j].at })

	m := newMatch("racing", matchID, start)
	m.add("", "MATCH_STARTED", map[string]any{"track": track, "laps": laps})
	for _, rc := range racers {
		m.add(rc.name, "PLAYER_JOINED", nil)
	}
	raceStart := m.clock.Add(5 * time.Second) // semáforo en verde
	for _, e := range pending {
		m.clock = raceStart.Add(e.at)
		m.add(e.player, e.typ, e.data)
	}
	if abandonAfterLap > 0 {
		return m.events // partida abandonada: nunca llega MATCH_FINISHED
	}

	winner, best := "", 0
	for _, rc := range racers {
		if t := totals[rc.name]; winner == "" || t < best {
			winner, best = rc.name, t
		}
	}
	m.wait(2 * time.Second)
	return m.finish(winner)
}

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }
