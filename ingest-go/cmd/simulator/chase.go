package main

import (
	"math/rand/v2"
	"time"
)

// Juego "chase": La Buena vs La Mala (el proyecto Java + Prolog de Jean).
//
// Un tablero de filas x columnas con obstáculos. La Buena (el jugador) parte
// de una celda y debe alcanzar a La Mala moviéndose arriba, abajo, izquierda
// o derecha, sin salir del tablero ni atravesar obstáculos. El juego calcula
// la ruta más corta (A* en Prolog); el jugador la recorre y puede equivocarse:
// desviarse (pasos de más) o chocar con un obstáculo (pierde una vida).
// Tiene 3 vidas. Coordenadas (fila, columna) desde 0, como en el juego.
//
// Eventos (ver docs/CONTRACT.md, sección 2.4):
//   MATCH_STARTED   data {rows, cols, obstaclePct, lives}
//   ROUTE_COMPUTED  playerId  data {found, start, goal, optimalSteps, routeTurns}
//   PLAYER_MOVED    playerId  data {row, col, direction, turn}
//   OBSTACLE_HIT    playerId  data {row, col, livesLeft}
//   TARGET_REACHED  playerId  data {steps, turns}

type cell struct{ row, col int }

type direction struct {
	name   string
	dr, dc int
}

// Los mismos nombres que usa el juego de Jean en "Movimientos: ...".
var directions = []direction{{"arriba", -1, 0}, {"abajo", 1, 0}, {"izquierda", 0, -1}, {"derecha", 0, 1}}

type board struct {
	rows, cols int
	blocked    [][]bool
}

func (b board) inside(c cell) bool {
	return c.row >= 0 && c.row < b.rows && c.col >= 0 && c.col < b.cols
}
func (b board) free(c cell) bool { return b.inside(c) && !b.blocked[c.row][c.col] }

// distances calcula, con una búsqueda en anchura (BFS) desde la meta, cuántos
// pasos faltan desde cada celda libre. -1 = la meta no es alcanzable desde ahí.
func (b board) distances(goal cell) [][]int {
	dist := make([][]int, b.rows)
	for i := range dist {
		dist[i] = make([]int, b.cols)
		for j := range dist[i] {
			dist[i][j] = -1
		}
	}
	dist[goal.row][goal.col] = 0
	queue := []cell{goal}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		for _, d := range directions {
			n := cell{c.row + d.dr, c.col + d.dc}
			if b.free(n) && dist[n.row][n.col] == -1 {
				dist[n.row][n.col] = dist[c.row][c.col] + 1
				queue = append(queue, n)
			}
		}
	}
	return dist
}

// bestStep devuelve el siguiente paso de la ruta más corta. Entre pasos igual
// de buenos prefiere seguir en la misma dirección (menos giros), igual que la
// heurística del juego de Jean.
func bestStep(b board, dist [][]int, from cell, lastDir string) (cell, direction) {
	var best cell
	var bestDir direction
	found := false
	for _, d := range directions {
		n := cell{from.row + d.dr, from.col + d.dc}
		if !b.free(n) || dist[n.row][n.col] != dist[from.row][from.col]-1 {
			continue
		}
		if !found || d.name == lastDir {
			best, bestDir, found = n, d, true
		}
	}
	return best, bestDir
}

// routeTurns cuenta los giros de la ruta que el juego le mostraría al jugador.
func routeTurns(b board, dist [][]int, start cell) int {
	turns, last := 0, ""
	for c := start; dist[c.row][c.col] > 0; {
		next, d := bestStep(b, dist, c, last)
		if last != "" && d.name != last {
			turns++
		}
		c, last = next, d.name
	}
	return turns
}

func randomBoard(r *rand.Rand, obstaclePct int) (board, cell, cell) {
	b := board{rows: 6 + r.IntN(5), cols: 8 + r.IntN(7)}
	b.blocked = make([][]bool, b.rows)
	for i := range b.blocked {
		b.blocked[i] = make([]bool, b.cols)
		for j := range b.blocked[i] {
			b.blocked[i][j] = r.IntN(100) < obstaclePct
		}
	}
	randomFree := func() cell {
		for {
			c := cell{r.IntN(b.rows), r.IntN(b.cols)}
			if b.free(c) {
				return c
			}
		}
	}
	start := randomFree()
	goal := randomFree()
	for abs(start.row-goal.row)+abs(start.col-goal.col) < (b.rows+b.cols)/3 {
		goal = randomFree() // que no aparezcan pegadas
	}
	return b, start, goal
}

type chaseMode int

const (
	chaseNormal  chaseMode = iota
	chasePerfect           // el jugador nunca se equivoca
	chaseNoRoute           // La Mala queda encerrada: no existe ruta
)

func simulateChase(r *rand.Rand, matchID string, start time.Time, mode chaseMode) []Event {
	m := newMatch("chase", matchID, start)
	player := pickPlayers(r, 1)[0]
	obstaclePct := 20 + r.IntN(11)
	lives := 3

	b, pos, goal := randomBoard(r, obstaclePct)
	dist := b.distances(goal)
	if mode == chaseNormal && dist[pos.row][pos.col] <= 0 {
		// Como en el juego real, a veces no hay ruta; un segundo intento lo vuelve
		// poco frecuente sin eliminarlo (la partida sin ruta sigue siendo posible).
		b, pos, goal = randomBoard(r, obstaclePct)
		dist = b.distances(goal)
	}
	if mode == chasePerfect {
		for dist[pos.row][pos.col] <= 0 { // otro tablero hasta que haya ruta
			b, pos, goal = randomBoard(r, obstaclePct)
			dist = b.distances(goal)
		}
	}
	if mode == chaseNoRoute { // rodear a La Mala de obstáculos
		for _, d := range directions {
			if n := (cell{goal.row + d.dr, goal.col + d.dc}); b.inside(n) && n != pos {
				b.blocked[n.row][n.col] = true
			}
		}
		dist = b.distances(goal)
	}

	m.add("", "MATCH_STARTED", map[string]any{"rows": b.rows, "cols": b.cols, "obstaclePct": obstaclePct, "lives": lives})
	m.wait(500 * time.Millisecond)
	m.add(player, "PLAYER_JOINED", nil)
	m.wait(between(r, 200*time.Millisecond, 800*time.Millisecond))

	route := map[string]any{"found": dist[pos.row][pos.col] > 0, "start": pos.asData(), "goal": goal.asData()}
	if dist[pos.row][pos.col] <= 0 {
		m.add(player, "ROUTE_COMPUTED", route)
		m.wait(2 * time.Second)
		return m.finish("") // sin ruta: nadie gana
	}
	route["optimalSteps"] = dist[pos.row][pos.col]
	route["routeTurns"] = routeTurns(b, dist, pos)
	m.add(player, "ROUTE_COMPUTED", route)

	skill := 0.7 + r.Float64()*0.28 // probabilidad de seguir la ruta correcta
	if mode == chasePerfect {
		skill = 1
	}

	steps, turns, last := 0, 0, ""
	maxSteps := 3*dist[pos.row][pos.col] + 10
	for steps < maxSteps && lives > 0 {
		m.wait(between(r, 400*time.Millisecond, 1500*time.Millisecond))
		next, d := bestStep(b, dist, pos, last)

		if r.Float64() >= skill { // el jugador se equivoca
			if hit, ok := blockedNeighbor(r, b, pos); ok && r.IntN(2) == 0 {
				lives--
				m.add(player, "OBSTACLE_HIT", map[string]any{"row": hit.row, "col": hit.col, "livesLeft": lives})
				continue
			}
			if alt, altDir, ok := detour(r, b, pos, next); ok {
				next, d = alt, altDir
			}
		}

		turn := last != "" && d.name != last
		if turn {
			turns++
		}
		steps++
		pos, last = next, d.name
		m.add(player, "PLAYER_MOVED", map[string]any{"row": pos.row, "col": pos.col, "direction": d.name, "turn": turn})

		if pos == goal {
			m.wait(300 * time.Millisecond)
			m.add(player, "TARGET_REACHED", map[string]any{"steps": steps, "turns": turns})
			m.wait(time.Second)
			return m.finish(player)
		}
	}
	m.wait(time.Second)
	return m.finish("") // sin vidas o demasiados pasos
}

// blockedNeighbor elige una celda vecina con obstáculo, si hay alguna.
func blockedNeighbor(r *rand.Rand, b board, from cell) (cell, bool) {
	var opts []cell
	for _, d := range directions {
		if n := (cell{from.row + d.dr, from.col + d.dc}); b.inside(n) && b.blocked[n.row][n.col] {
			opts = append(opts, n)
		}
	}
	if len(opts) == 0 {
		return cell{}, false
	}
	return opts[r.IntN(len(opts))], true
}

// detour elige una celda libre vecina que NO es la del camino correcto.
func detour(r *rand.Rand, b board, from, correct cell) (cell, direction, bool) {
	var cells []cell
	var dirs []direction
	for _, d := range directions {
		if n := (cell{from.row + d.dr, from.col + d.dc}); b.free(n) && n != correct {
			cells, dirs = append(cells, n), append(dirs, d)
		}
	}
	if len(cells) == 0 {
		return cell{}, direction{}, false
	}
	i := r.IntN(len(cells))
	return cells[i], dirs[i], true
}

func (c cell) asData() map[string]any { return map[string]any{"row": c.row, "col": c.col} }

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
