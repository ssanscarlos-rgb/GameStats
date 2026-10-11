package gamestats.analysis

import gamestats.model.*
import gamestats.analysis.Common.*
import java.time.{Duration, Instant}

/** Juego de combate (shooter). Eventos: SHOT_FIRED, DAMAGE_DEALT, PLAYER_ELIMINATED, MATCH_FINISHED. */
object CombatAnalyzer extends GameAnalyzer:
  val gameId = "combat"

  val StreakWindow: Duration = Duration.ofSeconds(10)
  val StreakLength           = 3
  val ComebackDeficit        = 3

  override def derive(c: Map[String, Double]): Map[String, Double] =
    val kills  = c.getOrElse("kills", 0.0)
    val deaths = c.getOrElse("deaths", 0.0)
    val shots  = c.getOrElse("shotsFired", 0.0)
    val hits   = c.getOrElse("shotsHit", 0.0)
    Map(
      "kd"              -> kills / math.max(deaths, 1.0),
      "accuracy"        -> (if shots == 0 then 0.0 else hits / shots),
      "avgDamagePerHit" -> (if hits == 0 then 0.0 else c.getOrElse("damageDealt", 0.0) / hits)
    )

  def analyze(matchId: String, events: List[GameEvent]): MatchAnalysis =
    val elims  = events.filter(_.`type` == "PLAYER_ELIMINATED")
    val damage = events.filter(_.`type` == "DAMAGE_DEALT")

    // Estadísticas agregadas por jugador
    val kills  = countBy(elims)(_.playerId)
    val deaths = countBy(elims)(_.str("victimId"))
    val shots  = countBy(events.filter(_.`type` == "SHOT_FIRED"))(_.playerId)
    val hits   = countBy(damage)(_.playerId)
    val dmg = damage
      .flatMap(e => e.playerId.map(_ -> e.num("amount").getOrElse(0.0)))
      .groupMapReduce(_._1)(_._2)(_ + _)

    val playerIds = (events.flatMap(_.playerId) ++ deaths.keys).distinct.sorted
    val players = playerIds.map { p =>
      val counters = Map(
        "kills"       -> kills.getOrElse(p, 0).toDouble,
        "deaths"      -> deaths.getOrElse(p, 0).toDouble,
        "shotsFired"  -> shots.getOrElse(p, 0).toDouble,
        "shotsHit"    -> hits.getOrElse(p, 0).toDouble,
        "damageDealt" -> dmg.getOrElse(p, 0.0)
      )
      PlayerMatchStats(p, counters, derive(counters))
    }

    val winner       = events.find(_.`type` == "MATCH_FINISHED").flatMap(_.str("winnerId"))
    val (start, end) = bounds(events)

    MatchAnalysis(
      matchId, gameId, start, end, durationSeconds(start, end), winner,
      eventCounts(events), players,
      patterns = killStreaks(elims) ++ comebacks(elims, winner)
    )

  /** Racha: >= StreakLength eliminaciones seguidas, separadas <= StreakWindow, sin morir en medio. */
  private def killStreaks(elims: List[GameEvent]): List[PatternDetection] =
    type State = (Map[String, (Int, Instant)], List[PatternDetection])
    val (_, found) = elims.foldLeft[State]((Map.empty, Nil)) { case ((streaks, acc), e) =>
      val cleared = e.str("victimId").fold(streaks)(streaks - _) // la víctima pierde su racha
      e.playerId match
        case None => (cleared, acc)
        case Some(killer) =>
          val n = cleared.get(killer) match
            case Some((count, last)) if Duration.between(last, e.timestamp).compareTo(StreakWindow) <= 0 =>
              count + 1
            case _ => 1
          val detection =
            if n == StreakLength then
              List(PatternDetection("KILL_STREAK", killer, Some(e.timestamp),
                s"$StreakLength eliminaciones seguidas (<= ${StreakWindow.getSeconds}s entre cada una) sin morir"))
            else Nil
          (cleared.updated(killer, (n, e.timestamp)), acc ++ detection)
    }
    found

  /** Remontada: el ganador llegó a ir perdiendo por >= ComebackDeficit eliminaciones. */
  private def comebacks(elims: List[GameEvent], winner: Option[String]): List[PatternDetection] =
    winner.toList.flatMap { w =>
      val progress = elims.scanLeft(Map.empty[String, Int]) { (acc, e) =>
        e.playerId.fold(acc)(k => acc.updated(k, acc.getOrElse(k, 0) + 1))
      }
      val maxDeficit = progress
        .map(m => m.removed(w).values.maxOption.getOrElse(0) - m.getOrElse(w, 0))
        .maxOption
        .getOrElse(0)
      if maxDeficit >= ComebackDeficit then
        List(PatternDetection("COMEBACK", w, elims.lastOption.map(_.timestamp),
          s"Ganó tras ir perdiendo por $maxDeficit eliminaciones"))
      else Nil
    }
