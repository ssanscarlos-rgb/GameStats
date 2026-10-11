# GameStats — Contrato de integración Go ↔ Scala

**Versión:** 0.1 (borrador para acordar entre Jean y Andrés)
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

### 2.3 Juego `racing` — propuesto, aún sin analizador

| `type` | `playerId` | `data` |
|---|---|---|
| `LAP_COMPLETED` | piloto | `lap` (int), `lapTimeMs` (int), `position` (int) |
| `POSITION_CHANGED` | piloto | `from` (int), `to` (int) |
| `PENALTY_APPLIED` | piloto | `seconds` (número), `reason` |

Estadísticas previstas: mejor vuelta, promedio, consistencia (desviación estándar), evolución de posición. Patrón previsto: remontada (ganador que estuvo fuera del top 3).

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

## 5. Estadísticas y reglas (`combat`)

| Nivel | Métrica / regla | Eventos usados |
|---|---|---|
| Jugador | kills, deaths, K/D | `PLAYER_ELIMINATED` |
| Jugador | daño total, daño promedio por impacto | `DAMAGE_DEALT` |
| Jugador (combina tipos) | precisión = impactos / disparos | `SHOT_FIRED` + `DAMAGE_DEALT` |
| Partida | duración, ganador, conteo por tipo | todos |
| Patrón | `KILL_STREAK`: 3+ eliminaciones, ≤10 s entre cada una, sin morir | secuencia de `PLAYER_ELIMINATED` |
| Patrón | `COMEBACK`: ganador que llegó a ir perdiendo por ≥3 eliminaciones | secuencia de `PLAYER_ELIMINATED` + `MATCH_FINISHED` |

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

1. ¿Segundo juego: `racing` (propuesto) u otro (ajedrez, cartas)?
2. ¿Cómo recibe Go los eventos: archivos JSON, API REST propia, o ambos?
3. ¿Cuándo considera Go "lista" una partida: al ver `MATCH_FINISHED`, o por inactividad con timeout?
4. ¿Los resultados persisten en disco o basta memoria? (Hoy: memoria; se pierden al reiniciar Scala.)
5. Nombre y puerto del servicio Go.
