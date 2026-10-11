package gamestats

import gamestats.analysis.Analyzers
import gamestats.model.*
import java.util.concurrent.atomic.AtomicReference

/** Almacén en memoria e inmutable (se reemplaza completo con AtomicReference). */
class ResultStore:
  private val matches = AtomicReference(Map.empty[String, MatchAnalysis])

  def save(results: List[MatchAnalysis]): Unit =
    matches.updateAndGet(_ ++ results.map(r => r.matchId -> r))

  def allMatches: List[MatchAnalysis] = matches.get.values.toList.sortBy(_.matchId)
  def findMatch(id: String): Option[MatchAnalysis] = matches.get.get(id)

  /** Suma contadores entre partidas y recalcula los ratios con el analizador del juego. */
  def playerSummaries: List[PlayerSummary] =
    allMatches
      .flatMap(m => m.players.map(p => (m, p)))
      .groupBy { case (m, p) => (m.gameId, p.playerId) }
      .toList
      .map { case ((game, player), rows) =>
        val counters = rows.map(_._2.counters).foldLeft(Map.empty[String, Double]) { (acc, c) =>
          c.foldLeft(acc) { case (a, (k, v)) => a.updated(k, a.getOrElse(k, 0.0) + v) }
        }
        val patterns = rows.flatMap(_._1.patterns).filter(_.playerId == player)
        PlayerSummary(
          game, player,
          matchesPlayed = rows.size,
          wins = rows.count(_._1.winnerId.contains(player)),
          counters, Analyzers.forGame(game).derive(counters),
          patternCounts = patterns.groupMapReduce(_.rule)(_ => 1)(_ + _)
        )
      }
      .sortBy(s => (s.gameId, s.playerId))
