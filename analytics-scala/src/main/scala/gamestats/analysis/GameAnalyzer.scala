package gamestats.analysis

import gamestats.model.*
import java.time.Instant

/** Un analizador por juego. Para soportar un juego nuevo: crear un objeto y registrarlo en `Analyzers`. */
trait GameAnalyzer:
  def gameId: String
  def analyze(matchId: String, events: List[GameEvent]): MatchAnalysis

  /** Calcula ratios a partir de contadores (se reutiliza para partida y para agregado global). */
  def derive(counters: Map[String, Double]): Map[String, Double] = Map.empty

object Common:
  def countBy[A](xs: List[A])(f: A => Option[String]): Map[String, Int] =
    xs.flatMap(f).groupMapReduce(identity)(_ => 1)(_ + _)

  def eventCounts(events: List[GameEvent]): Map[String, Int] =
    events.groupMapReduce(_.`type`)(_ => 1)(_ + _)

  def bounds(events: List[GameEvent]): (Option[Instant], Option[Instant]) =
    val ts = events.map(_.timestamp)
    (ts.minOption, ts.maxOption)

  def durationSeconds(start: Option[Instant], end: Option[Instant]): Long =
    (start, end) match
      case (Some(s), Some(e)) => java.time.Duration.between(s, e).getSeconds
      case _                  => 0L

object Analyzers:
  private val registry: Map[String, GameAnalyzer] =
    List[GameAnalyzer](CombatAnalyzer).map(a => a.gameId -> a).toMap

  def forGame(gameId: String): GameAnalyzer = registry.getOrElse(gameId, GenericAnalyzer)

  /** Agrupa por (juego, partida) y delega en el analizador correspondiente. */
  def analyze(events: List[GameEvent]): List[MatchAnalysis] =
    events
      .groupBy(e => (e.gameId, e.matchId))
      .toList
      .map { case ((game, matchId), evs) =>
        forGame(game).analyze(matchId, evs.sortBy(_.timestamp))
      }
