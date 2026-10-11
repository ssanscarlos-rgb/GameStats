package gamestats.model

import io.circe.{Codec, Json}
import java.time.Instant

/** Evento genérico: campos comunes + `data` libre específico de cada juego/acción. */
case class GameEvent(
    eventId: String,
    timestamp: Instant,
    gameId: String,
    gameVersion: Option[String],
    matchId: String,
    playerId: Option[String],
    `type`: String,
    action: Option[String],
    data: Option[Json]
) derives Codec.AsObject:
  def str(key: String): Option[String] = data.flatMap(_.hcursor.get[String](key).toOption)
  def num(key: String): Option[Double] = data.flatMap(_.hcursor.get[Double](key).toOption)

/** Lo que Go envía a POST /analyze. Puede traer eventos de varias partidas/juegos. */
case class AnalyzeRequest(events: List[GameEvent]) derives Codec.AsObject

/** Resultado de una regla/patrón detectado sobre una secuencia de eventos. */
case class PatternDetection(rule: String, playerId: String, at: Option[Instant], details: String)
    derives Codec.AsObject

/** counters = valores aditivos (se pueden sumar entre partidas); derived = ratios calculados desde counters. */
case class PlayerMatchStats(
    playerId: String,
    counters: Map[String, Double],
    derived: Map[String, Double]
) derives Codec.AsObject

case class MatchAnalysis(
    matchId: String,
    gameId: String,
    startedAt: Option[Instant],
    finishedAt: Option[Instant],
    durationSeconds: Long,
    winnerId: Option[String],
    eventCounts: Map[String, Int],
    players: List[PlayerMatchStats],
    patterns: List[PatternDetection]
) derives Codec.AsObject

/** Agregado por jugador a través de todas las partidas analizadas de un juego. */
case class PlayerSummary(
    gameId: String,
    playerId: String,
    matchesPlayed: Int,
    wins: Int,
    counters: Map[String, Double],
    derived: Map[String, Double],
    patternCounts: Map[String, Int]
) derives Codec.AsObject
