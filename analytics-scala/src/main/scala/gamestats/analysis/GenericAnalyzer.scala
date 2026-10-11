package gamestats.analysis

import gamestats.model.*
import gamestats.analysis.Common.*

/** Fallback para juegos sin analizador propio: solo conteos de eventos por tipo y por jugador. */
object GenericAnalyzer extends GameAnalyzer:
  val gameId = "*"

  def analyze(matchId: String, events: List[GameEvent]): MatchAnalysis =
    val (start, end) = bounds(events)
    val players = events
      .flatMap(_.playerId)
      .distinct
      .sorted
      .map { p =>
        val mine = events.filter(_.playerId.contains(p))
        val counters = eventCounts(mine).map((t, n) => s"events_$t" -> n.toDouble) +
          ("events_total" -> mine.size.toDouble)
        PlayerMatchStats(p, counters, Map.empty)
      }
    MatchAnalysis(
      matchId, events.headOption.map(_.gameId).getOrElse("unknown"),
      start, end, durationSeconds(start, end),
      winnerId = events.find(_.`type` == "MATCH_FINISHED").flatMap(_.str("winnerId")),
      eventCounts(events), players, Nil
    )
