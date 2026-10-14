# GameStats — Contrato de integración Go ↔ Scala

**Versión:** 0.2 (borrador para acordar entre Jean y Andrés; agrega `racing` y `chase`)
**Transporte:** HTTP/REST con JSON (UTF-8, `Content-Type: application/json`)
**Servicio Scala (análisis):** `http://analyzer:8081` (variable `PORT`)
**Cliente:** el módulo Go (ingestión) llama a Scala. Scala nunca llama a Go.

Justificación técnica de HTTP/REST: ambos lenguajes lo soportan en su librería estándar o con dependencias mínimas, es fácil de probar con `curl`, funciona igual en Docker Compose, y un componente externo puede producir eventos sin conocer la implementación interna.

---

## 1. Flujo general

```
Fuente externa (simulador) → Go: recibe, valida, agrupa por partida (goroutines/channels)
                           → Go: POST /analyze (partida completa) → Scala: analiza y guarda
Consumidor / UI            → Scala: GET /results/...
```

| Responsabilidad | Go | Scala |
|---|---|---|
| Recibir eventos de la fuente | ✅ | |
| Validar estructura básica y descartar inválidos | ✅ | |
| Eliminar duplicados (`eventId`) | ✅ | |
| Agrupar por partida, decidir cuándo está lista | ✅ | |
| Ordenar por `timestamp`, agrupar, calcular estadísticas | | ✅ |
| Detectar patrones sobre secuencias | | ✅ |
| Exponer resultados | | ✅ |

---

## 2. Formato de evento

```json
{
  "eventId": "c1-007",
  "timestamp": "2026-09-01T10:00:20Z",
  "gameId": "combat",
  "gameVersion": "1.0",
  "matchId": "combat-001",
  "playerId": "bob",
  "type": "PLAYER_ELIMINATED",
  "action": null,
  "data": { "victimId": "alice", "weapon": "rifle" }
}
```

| Campo | Tipo | Obligatorio | Reglas |
|---|---|---|---|
| `eventId` | string | **sí** | No vacío, único globalmente |
| `timestamp` | string | **sí** | ISO-8601 en UTC con `Z` (ej. `2026-09-01T10:00:20Z`; admite fracciones `…20.350Z`) |
| `gameId` | string | **sí** | Minúsculas, ej. `combat`, `racing`. Scala elige el analizador con este campo |
| `gameVersion` | string | no | |
| `matchId` | string | **sí** | Único globalmente. Una partida pertenece a un solo `gameId` |
| `playerId` | string | no | Ausente en eventos de partida (`MATCH_STARTED`, `MATCH_FINISHED`) |
| `type` | string | **sí** | `MAYUSCULAS_CON_GUION_BAJO` |
| `action` | string | no | Subtipo opcional |
| `data` | objeto | no | Libre, depende del juego/tipo. Valores numéricos como número JSON, no string |

Los campos opcionales pueden omitirse o ir en `null`.

### 2.1 Eventos generales (todos los juegos)

| `type` | `playerId` | `data` |
|---|---|---|
| `MATCH_STARTED` | – | – |
| `PLAYER_JOINED` | jugador | – |
| `PLAYER_LEFT` | jugador | – |
| `MATCH_FINISHED` | – | `winnerId` (string, opcional) |

### 2.2 Juego `combat` — implementado en Scala

| `type` | `playerId` | `data` |
|---|---|---|
| `SHOT_FIRED` | tirador | `weapon` |
| `DAMAGE_DEALT` | atacante | `target`, `amount` (número), `weapon` |
| `PLAYER_ELIMINATED` | **asesino** | `victimId`, `weapon` |

> En `PLAYER_ELIMINATED` el `playerId` es quien elimina y la víctima va en `data.victimId`.

### 2.3 Juego `racing` — carrera por vueltas (sin analizador propio todavía)

Diseñado para ser lo más sencillo de implementar como juego real: el juego solo avisa cuando un piloto completa una vuelta y cuando choca. Las posiciones, la mejor vuelta y las remontadas **no** las manda el juego: Scala las calcula a partir de los tiempos.

| `type` | `playerId` | `data` |
|---|---|---|
| `MATCH_STARTED` | – | `track` (string), `laps` (int) |
| `PLAYER_JOINED` | piloto | – |
| `CRASH` | piloto | `lap` (int), `timeLostMs` (int) |
| `LAP_COMPLETED` | piloto | `lap` (int), `lapTimeMs` (int, **ya incluye** el tiempo perdido por choques) |
| `MATCH_FINISHED` | – | `winnerId` = piloto con menor tiempo total |

Ejemplo: `samples/racing_match.json` (vale choca en la vuelta 1, queda última y gana).

Estadísticas y reglas: ver sección 5.2.

### 2.4 Juego `chase` — La Buena vs La Mala (sin analizador propio todavía)

Basado en el proyecto Java + Prolog de Jean. Tablero de `rows` × `cols` con obstáculos; La Buena (el jugador) debe alcanzar a La Mala moviéndose arriba, abajo, izquierda o derecha. El juego calcula la ruta más corta (A\*) y el jugador la recorre; si choca con un obstáculo pierde una vida (3 en total). Coordenadas `(row, col)` desde 0. Un solo jugador por partida.

| `type` | `playerId` | `data` |
|---|---|---|
| `MATCH_STARTED` | – | `rows`, `cols`, `obstaclePct`, `lives` (int) |
| `PLAYER_JOINED` | jugador | – |
| `ROUTE_COMPUTED` | jugador | `found` (bool), `start` y `goal` (`{row, col}`), `optimalSteps` y `routeTurns` (int, solo si `found`) |
| `PLAYER_MOVED` | jugador | `row`, `col` (int, celda de llegada), `direction` (`arriba`/`abajo`/`izquierda`/`derecha`), `turn` (bool, cambió de dirección) |
| `OBSTACLE_HIT` | jugador | `row`, `col` (celda del obstáculo), `livesLeft` (int) |
| `TARGET_REACHED` | jugador | `steps`, `turns` (int) |
| `MATCH_FINISHED` | – | `winnerId` solo si alcanzó a La Mala |

Ejemplo: `samples/chase_match.json`.

Estadísticas y reglas: ver sección 5.3.

### 2.5 Juegos sin analizador

Si Scala no tiene analizador para un `gameId`, usa `GenericAnalyzer` (conteos por jugador y por tipo de evento). Go no conoce ningún juego en particular: **agregar un juego no requiere cambios en el servicio de ingestión**, solo en el simulador (la fuente de eventos) y, si se quieren estadísticas propias, un analizador en Scala.

---

## 3. Endpoints de Scala

### 3.1 `POST /analyze`

Envía eventos para analizar. Puede contener eventos de **varias partidas y juegos**; Scala agrupa por `(gameId, matchId)`.

**Request**
```json
{ "events": [ { ...evento... }, { ...evento... } ] }
```

**Respuesta 200**
```json
{ "analyzedMatches": ["combat-001"] }
```

**Respuesta 400** — JSON mal formado, campo obligatorio faltante o `timestamp` inválido. **Se rechaza toda la petición**, por eso Go debe validar antes de enviar.
```json
{ "error": "DecodingFailure at .events[3].timestamp: ..." }
```

**Reglas importantes**
- Enviar la partida **completa** en una sola llamada (idealmente terminada con `MATCH_FINISHED`). Reenviar un `matchId` **reemplaza** el análisis anterior; no acumula.
- El orden dentro del arreglo no importa; Scala ordena por `timestamp`.
- `events` vacío es válido y no genera resultados.

### 3.2 `GET /results/matches`
Lista de análisis de todas las partidas (esquema en 4.1).

### 3.3 `GET /results/match/{matchId}`
Un análisis de partida. `404` con `{"error":"match not found"}` si no existe.

### 3.4 `GET /results/players`
Agregado por jugador y juego, sumando todas las partidas (esquema en 4.2).

### 3.5 `GET /health`
`200 {"status":"ok"}`. Para healthchecks de Docker/Compose y para que Go espere a Scala antes de enviar.

---

## 4. Formato de resultados

### 4.1 Análisis de partida (`MatchAnalysis`)

Resultado real esperado para `samples/combat_match.json`:

```json
{
  "matchId": "combat-001",
  "gameId": "combat",
  "startedAt": "2026-09-01T10:00:00Z",
  "finishedAt": "2026-09-01T10:02:40Z",
  "durationSeconds": 160,
  "winnerId": "alice",
  "eventCounts": {
    "MATCH_STARTED": 1, "PLAYER_JOINED": 2, "SHOT_FIRED": 8,
    "DAMAGE_DEALT": 7, "PLAYER_ELIMINATED": 7, "MATCH_FINISHED": 1
  },
  "players": [
    {
      "playerId": "alice",
      "counters": { "kills": 4, "deaths": 3, "shotsFired": 5, "shotsHit": 4, "damageDealt": 400 },
      "derived":  { "kd": 1.3333, "accuracy": 0.8, "avgDamagePerHit": 100 }
    },
    {
      "playerId": "bob",
      "counters": { "kills": 3, "deaths": 4, "shotsFired": 3, "shotsHit": 3, "damageDealt": 300 },
      "derived":  { "kd": 0.75, "accuracy": 1.0, "avgDamagePerHit": 100 }
    }
  ],
  "patterns": [
    { "rule": "KILL_STREAK", "playerId": "bob",   "at": "2026-09-01T10:00:20Z",
      "details": "3 eliminaciones seguidas (<= 10s entre cada una) sin morir" },
    { "rule": "COMEBACK",    "playerId": "alice", "at": "2026-09-01T10:02:30Z",
      "details": "Ganó tras ir perdiendo por 3 eliminaciones" }
  ]
}
```

- `counters`: valores aditivos (se pueden sumar entre partidas).
- `derived`: ratios calculados a partir de `counters`.
- Las claves de `counters`/`derived` dependen del juego.

### 4.2 Resumen de jugador (`PlayerSummary`)

```json
{
  "gameId": "combat", "playerId": "alice",
  "matchesPlayed": 1, "wins": 1,
  "counters": { "kills": 4, "deaths": 3, "shotsFired": 5, "shotsHit": 4, "damageDealt": 400 },
  "derived":  { "kd": 1.3333, "accuracy": 0.8, "avgDamagePerHit": 100 },
  "patternCounts": { "COMEBACK": 1 }
}
```

---

## 5. Estadísticas y reglas

### 5.1 `combat`

| Nivel | Métrica / regla | Eventos usados |
|---|---|---|
| Jugador | kills, deaths, K/D | `PLAYER_ELIMINATED` |
| Jugador | daño total, daño promedio por impacto | `DAMAGE_DEALT` |
| Jugador (combina tipos) | precisión = impactos / disparos | `SHOT_FIRED` + `DAMAGE_DEALT` |
| Partida | duración, ganador, conteo por tipo | todos |
| Patrón | `KILL_STREAK`: 3+ eliminaciones, ≤10 s entre cada una, sin morir | secuencia de `PLAYER_ELIMINATED` |
| Patrón | `COMEBACK`: ganador que llegó a ir perdiendo por ≥3 eliminaciones | secuencia de `PLAYER_ELIMINATED` + `MATCH_FINISHED` |

### 5.2 `racing`

Las posiciones no vienen en los eventos: la posición de un piloto tras la vuelta *k* es su lugar al ordenar a todos por tiempo acumulado (suma de `lapTimeMs` hasta la vuelta *k*).

| Nivel | Métrica / regla | Eventos usados |
|---|---|---|
| Jugador | vueltas completadas, tiempo total | `LAP_COMPLETED` |
| Jugador | tiempo promedio por vuelta, consistencia (desviación estándar de sus vueltas) | `LAP_COMPLETED` |
| Jugador | choques, tiempo perdido | `CRASH` |
| Jugador (combina tipos) | tasa de choques = choques / vueltas; % del tiempo perdido en choques | `CRASH` + `LAP_COMPLETED` |
| Jugador (secuencia) | podios (terminó 1.º a 3.º), posiciones ganadas (posición tras la vuelta 1 − posición final) | secuencia de `LAP_COMPLETED` |
| Partida | duración, ganador, conteo por tipo | todos |
| Partida | `FASTEST_LAP`: piloto con la vuelta más rápida de la carrera | `LAP_COMPLETED` |
| Patrón | `COMEBACK`: el ganador estuvo en el último lugar después de alguna vuelta | secuencia de `LAP_COMPLETED` + `MATCH_FINISHED` |
| Patrón | `CLEAN_RACE`: terminó en el podio sin ningún choque | `CRASH` + `LAP_COMPLETED` |

**`counters`** (aditivos): `lapsCompleted`, `totalTimeMs`, `lapTimeSqSum` (suma de los cuadrados de cada vuelta; permite calcular la consistencia después de sumar varias carreras), `crashes`, `timeLostMs`, `podiums`, `positionsGained`.

**`derived`**:
- `avgLapMs` = `totalTimeMs / lapsCompleted`
- `consistencyMs` = √(`lapTimeSqSum / lapsCompleted` − `avgLapMs`²)
- `crashRate` = `crashes / lapsCompleted`
- `timeLostPct` = `timeLostMs / totalTimeMs`

**Resultado esperado para `samples/racing_match.json`** (4 vueltas, gana vale):

| Piloto | lapsCompleted | totalTimeMs | crashes | timeLostMs | podiums | positionsGained | avgLapMs | consistencyMs | crashRate | timeLostPct |
|---|---|---|---|---|---|---|---|---|---|---|
| vale | 4 | 244335 | 1 | 12000 | 1 | 3 | 61083.75 | 5460.9 | 0.25 | 0.0491 |
| luis | 4 | 252190 | 0 | 0 | 1 | -1 | 63047.5 | 362.7 | 0 | 0 |
| sofi | 4 | 255858 | 0 | 0 | 1 | -1 | 63964.5 | 248.7 | 0 | 0 |
| leo | 4 | 262175 | 0 | 0 | 0 | -1 | 65543.75 | 107.4 | 0 | 0 |

Orden tras la vuelta 1: luis, sofi, leo, vale. Orden final: vale, luis, sofi, leo.
Patrones: `COMEBACK` (vale), `FASTEST_LAP` (vale, vuelta 3, 57788 ms), `CLEAN_RACE` (luis y sofi).

### 5.3 `chase` (La Buena vs La Mala)

Una partida tiene un solo jugador. Una partida sin ruta (`found: false`) no cuenta en contra del jugador.

| Nivel | Métrica / regla | Eventos usados |
|---|---|---|
| Jugador | partidas con ruta, partidas ganadas, movimientos, choques | `ROUTE_COMPUTED`, `TARGET_REACHED`, `PLAYER_MOVED`, `OBSTACLE_HIT` |
| Jugador (combina tipos) | tasa de éxito = ganadas / partidas con ruta | `ROUTE_COMPUTED` + `TARGET_REACHED` |
| Jugador (combina tipos) | eficiencia = pasos óptimos / pasos usados (solo partidas ganadas) | `ROUTE_COMPUTED` + `TARGET_REACHED` |
| Jugador (combina tipos) | giros de más por victoria = giros usados − giros de la ruta | `ROUTE_COMPUTED` + `TARGET_REACHED` |
| Partida | duración, ganador (vacío si perdió), conteo por tipo | todos |
| Patrón | `PERFECT_RUN`: ganó con `steps == optimalSteps` y sin choques | `ROUTE_COMPUTED` + `OBSTACLE_HIT` + `TARGET_REACHED` |
| Patrón | `CLOSE_CALL`: ganó después de quedar con 1 sola vida | secuencia de `OBSTACLE_HIT` → `TARGET_REACHED` |
| Patrón | `NO_ROUTE`: el tablero no tenía ruta posible | `ROUTE_COMPUTED` con `found: false` |

**`counters`** (aditivos): `routeFound` (0 o 1), `reached` (0 o 1), `moves`, `obstacleHits`, y solo en partidas ganadas: `reachedSteps`, `reachedOptimalSteps`, `reachedTurns`, `reachedRouteTurns`.

**`derived`**:
- `successRate` = `reached / routeFound`
- `efficiency` = `reachedOptimalSteps / reachedSteps`
- `extraTurnsPerWin` = (`reachedTurns − reachedRouteTurns`) / `reached`
- `hitsPerMatch` = `obstacleHits / routeFound`

**Resultado esperado para `samples/chase_match.json`** (jugadora nati, ruta óptima de 7 pasos y 1 giro):

| Jugador | routeFound | reached | moves | obstacleHits | reachedSteps | reachedOptimalSteps | reachedTurns | reachedRouteTurns | successRate | efficiency | extraTurnsPerWin | hitsPerMatch |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| nati | 1 | 1 | 9 | 1 | 9 | 7 | 4 | 1 | 1.0 | 0.7778 | 3.0 | 1.0 |

Patrones: ninguno (no fue perfecta y le quedaron 2 vidas).

---

## 6. Ejemplos de uso

```bash
curl -X POST http://localhost:8081/analyze \
     -H 'Content-Type: application/json' \
     --data @samples/combat_match.json

curl http://localhost:8081/results/match/combat-001
curl http://localhost:8081/results/players
```

### Sugerencia de struct en Go (para Andrés)

```go
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
}

type AnalyzeRequest struct {
    Events []Event `json:"events"`
}
```

Usar `json.RawMessage` para `data` mantiene a Go genérico: no necesita conocer los campos de cada juego.

---

## 7. Docker Compose (previsto)

- `analyzer` (Scala): puerto 8081, healthcheck `GET /health`.
- `ingest` (Go): `depends_on: analyzer (service_healthy)`, variable `ANALYZER_URL=http://analyzer:8081`.

---

## 8. Decisiones pendientes

1. ~~¿Segundo juego?~~ Propuesta: `racing` (2.3) y `chase` (2.4). Pendiente: ¿cuáles tendrán analizador propio en Scala?
2. ~~¿Cómo recibe Go los eventos?~~ API REST propia: `POST http://ingest:8080/events` (un evento, un arreglo o `{"events":[...]}`).
3. ~~¿Cuándo está "lista" una partida?~~ Al recibir `MATCH_FINISHED`; como respaldo, tras `IDLE_TIMEOUT` (30 s) sin eventos.
4. ¿Los resultados persisten en disco o basta memoria? (Hoy: memoria; se pierden al reiniciar Scala.)
5. ~~Nombre y puerto del servicio Go.~~ `ingest`, puerto 8080.
