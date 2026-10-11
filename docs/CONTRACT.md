# Contrato Go ↔ Scala (borrador — acordar con Andrés)

Comunicación: **HTTP/REST + JSON**. Scala expone el servicio de análisis (puerto 8081); Go lo llama.

## Evento (formato de entrada)

| Campo        | Tipo            | Oblig. | Descripción |
|--------------|-----------------|--------|-------------|
| eventId      | string          | sí     | Identificador único |
| timestamp    | string ISO-8601 | sí     | Ej. `2026-09-01T10:00:20Z` |
| gameId       | string          | sí     | Ej. `combat` (Scala elige el analizador con este campo) |
| gameVersion  | string          | no     | |
| matchId      | string          | sí     | Único globalmente |
| playerId     | string          | no     | Ausente en eventos de partida (MATCH_STARTED/FINISHED) |
| type         | string          | sí     | Ej. `PLAYER_ELIMINATED` |
| action       | string          | no     | |
| data         | objeto libre    | no     | Específico del juego |

### Juego `combat` (propuesto)
| type | playerId | data |
|------|----------|------|
| MATCH_STARTED | – | – |
| PLAYER_JOINED | jugador | – |
| SHOT_FIRED | tirador | `weapon` |
| DAMAGE_DEALT | atacante | `target`, `amount`, `weapon` |
| PLAYER_ELIMINATED | **asesino** | `victimId`, `weapon` |
| MATCH_FINISHED | – | `winnerId` |

## Endpoints Scala

| Método | Ruta | Descripción |
|--------|------|-------------|
| POST | `/analyze` | Body `{"events":[...]}`. Acepta eventos de una o varias partidas/juegos (Scala agrupa). Responde `{"analyzedMatches":[ids]}`; 400 si el JSON es inválido |
| GET | `/results/matches` | Análisis de todas las partidas |
| GET | `/results/match/{matchId}` | Análisis de una partida |
| GET | `/results/players` | Agregado por jugador entre partidas |
| GET | `/health` | Estado |

## Responsabilidades
- **Go**: leer/recibir, validar (descartar y registrar inválidos), agrupar por partida con goroutines/channels, decidir cuándo una partida está lista y enviarla (idealmente validada y completa, con `MATCH_FINISHED`).
- **Scala**: ordenar por timestamp, agrupar, calcular estadísticas, detectar patrones, devolver JSON.
