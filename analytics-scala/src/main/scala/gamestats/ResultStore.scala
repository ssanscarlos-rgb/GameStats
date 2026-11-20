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

  /** Resumen por juego y general. Los ratios de cada juego se recalculan con su analizador. */
  def overview: ResultsOverview =
    val all   = allMatches
    val games = all.groupBy(_.gameId).toList.sortBy(_._1).map { (game, ms) => summarize(game, ms) }
    ResultsOverview(
      games,
      OverallSummary(
        gamesCount = games.size,
        matchesAnalyzed = all.size,
        matchesWithWinner = all.count(_.winnerId.isDefined),
        playersCount = distinctPlayers(all),
        totalDurationSeconds = all.map(_.durationSeconds).sum,
        avgDurationSeconds = average(all.map(_.durationSeconds).sum, all.size),
        eventCounts = sumMaps(all.map(_.eventCounts)),
        patternCounts = patternCounts(all)
      )
    )

  private def summarize(game: String, ms: List[MatchAnalysis]): GameSummary =
    val counters = sumMaps(ms.flatMap(_.players.map(_.counters)))
    val total    = ms.map(_.durationSeconds).sum
    GameSummary(
      gameId = game,
      matchesAnalyzed = ms.size,
      matchesWithWinner = ms.count(_.winnerId.isDefined),
      playersCount = distinctPlayers(ms),
      totalDurationSeconds = total,
      avgDurationSeconds = average(total, ms.size),
      eventCounts = sumMaps(ms.map(_.eventCounts)),
      patternCounts = patternCounts(ms),
      counters = counters,
      derived = Analyzers.forGame(game).derive(counters)
    )

  private def distinctPlayers(ms: List[MatchAnalysis]): Int =
    ms.flatMap(_.players.map(_.playerId)).distinct.size

  private def patternCounts(ms: List[MatchAnalysis]): Map[String, Int] =
    ms.flatMap(_.patterns).groupMapReduce(_.rule)(_ => 1)(_ + _)

  private def average(total: Long, n: Int): Double = if n == 0 then 0.0 else total.toDouble / n

  /** Suma mapas clave a clave (sirve para Int y Double). */
  private def sumMaps[V](maps: List[Map[String, V]])(using num: Numeric[V]): Map[String, V] =
    maps.foldLeft(Map.empty[String, V]) { (acc, m) =>
      m.foldLeft(acc) { case (a, (k, v)) => a.updated(k, num.plus(a.getOrElse(k, num.zero), v)) }
    }
