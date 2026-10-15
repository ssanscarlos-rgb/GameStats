package gamestats.analysis

import gamestats.model.*
import gamestats.analysis.Common.*
import java.time.Instant

/** Carrera por vueltas (ver docs/CONTRACT.md, secciones 2.3 y 5.2).
 *
 * Eventos: LAP_COMPLETED {lap, lapTimeMs}, CRASH {lap, timeLostMs}, MATCH_FINISHED {winnerId}.
 * El juego no manda posiciones: se calculan aquí sumando los tiempos de vuelta.
 */
object RacingAnalyzer extends GameAnalyzer:
  val gameId = "racing"

  val PodiumSize = 3

  /** Una vuelta ya leída del evento, con los datos que importan. */
  private case class Lap(player: String, lap: Int, timeMs: Double, at: Instant)

  override def derive(c: Map[String, Double]): Map[String, Double] =
    val laps  = c.getOrElse("lapsCompleted", 0.0)
    val total = c.getOrElse("totalTimeMs", 0.0)
    val avg   = if laps == 0 then 0.0 else total / laps
    // Varianza = promedio de los cuadrados - cuadrado del promedio. Funciona con
    // counters sumados de varias carreras porque lapTimeSqSum también es aditivo.
    val variance =
      if laps == 0 then 0.0
      else math.max(c.getOrElse("lapTimeSqSum", 0.0) / laps - avg * avg, 0.0)
    Map(
      "avgLapMs"      -> avg,
      "consistencyMs" -> math.sqrt(variance),
      "crashRate"     -> (if laps == 0 then 0.0 else c.getOrElse("crashes", 0.0) / laps),
      "timeLostPct"   -> (if total == 0 then 0.0 else c.getOrElse("timeLostMs", 0.0) / total)
    )

  def analyze(matchId: String, events: List[GameEvent]): MatchAnalysis =
    // collect = filter + map: solo las vueltas con piloto, ya convertidas en Lap.
    val laps: List[Lap] = events.collect {
      case e @ GameEvent(_, at, _, _, _, Some(p), "LAP_COMPLETED", _, _) =>
        Lap(p, e.num("lap").map(_.toInt).getOrElse(0), e.num("lapTimeMs").getOrElse(0.0), at)
    }
    val crashes = events.filter(_.`type` == "CRASH")

    val lapsByPlayer = laps.groupBy(_.player)
    val crashCount   = countBy(crashes)(_.playerId)
    val timeLost = crashes
      .flatMap(e => e.playerId.map(_ -> e.num("timeLostMs").getOrElse(0.0)))
      .groupMapReduce(_._1)(_._2)(_ + _)

    val playerIds = events.flatMap(_.playerId).distinct.sorted
    val finished  = events.exists(_.`type` == "MATCH_FINISHED")
    val winner    = events.find(_.`type` == "MATCH_FINISHED").flatMap(_.str("winnerId"))

    val standings  = standingsByLap(lapsByPlayer)
    val finalOrder = finishOrder(playerIds, lapsByPlayer)
    def finalPos(p: String): Int = finalOrder.indexOf(p) + 1
    def posAfterLap1(p: String): Option[Int] =
      standings.headOption.map(_.indexOf(p)).filter(_ >= 0).map(_ + 1)

    val players = playerIds.map { p =>
      val mine = lapsByPlayer.getOrElse(p, Nil).map(_.timeMs)
      val counters = Map(
        "lapsCompleted"   -> mine.size.toDouble,
        "totalTimeMs"     -> mine.sum,
        "lapTimeSqSum"    -> mine.map(t => t * t).sum,
        "crashes"         -> crashCount.getOrElse(p, 0).toDouble,
        "timeLostMs"      -> timeLost.getOrElse(p, 0.0),
        "podiums"         -> (if finished && finalPos(p) <= PodiumSize then 1.0 else 0.0),
        "positionsGained" -> posAfterLap1(p).fold(0.0)(first => (first - finalPos(p)).toDouble)
      )
      PlayerMatchStats(p, counters, derive(counters))
    }

    val (start, end) = bounds(events)
    val finishAt     = events.find(_.`type` == "MATCH_FINISHED").map(_.timestamp)

    MatchAnalysis(
      matchId, gameId, start, end, durationSeconds(start, end), winner,
      eventCounts(events), players,
      patterns = fastestLap(laps) ++ comeback(standings, winner, finishAt) ++ cleanRaces(players, finishAt)
    )

  /** Orden de los pilotos después de cada vuelta k: por tiempo acumulado hasta esa vuelta.
   * standings(0) es el orden tras la vuelta 1, standings(1) tras la vuelta 2, etc.
   */
  private def standingsByLap(lapsByPlayer: Map[String, List[Lap]]): List[List[String]] =
    // Por piloto: (vuelta, tiempo acumulado hasta esa vuelta). scanLeft va sumando.
    val cumulative: Map[String, List[(Int, Double)]] =
      lapsByPlayer.map { (p, ls) =>
        p -> ls.sortBy(_.lap).scanLeft((0, 0.0))((acc, l) => (l.lap, acc._2 + l.timeMs)).tail
      }
    val maxLap = lapsByPlayer.values.flatten.map(_.lap).maxOption.getOrElse(0)
    (1 to maxLap).toList.map { k =>
      cumulative.toList
        .flatMap((p, cs) => cs.find(_._1 == k).map(c => p -> c._2))
        .sortBy(_._2)
        .map(_._1)
    }

  /** Orden final: primero quien completó más vueltas; a igualdad, menor tiempo total. */
  private def finishOrder(players: List[String], lapsByPlayer: Map[String, List[Lap]]): List[String] =
    players.sortBy { p =>
      val ls = lapsByPlayer.getOrElse(p, Nil)
      (-ls.size, if ls.isEmpty then Double.MaxValue else ls.map(_.timeMs).sum)
    }

  /** FASTEST_LAP: la vuelta más rápida de toda la carrera. */
  private def fastestLap(laps: List[Lap]): List[PatternDetection] =
    laps.minByOption(_.timeMs).toList.map { l =>
      PatternDetection("FASTEST_LAP", l.player, Some(l.at),
        s"Vuelta más rápida: vuelta ${l.lap} en ${l.timeMs.toLong} ms")
    }

  /** COMEBACK: el ganador estuvo en el último lugar después de alguna vuelta. */
  private def comeback(standings: List[List[String]], winner: Option[String],
                       at: Option[Instant]): List[PatternDetection] =
    winner.toList.flatMap { w =>
      standings.zipWithIndex.collectFirst {
        case (order, i) if order.size > 1 && order.lastOption.contains(w) =>
          PatternDetection("COMEBACK", w, at, s"Ganó después de ir último tras la vuelta ${i + 1}")
      }
    }

  /** CLEAN_RACE: terminó en el podio sin ningún choque. */
  private def cleanRaces(players: List[PlayerMatchStats], at: Option[Instant]): List[PatternDetection] =
    players.collect {
      case p if p.counters.getOrElse("podiums", 0.0) == 1.0 && p.counters.getOrElse("crashes", 0.0) == 0.0 =>
        PatternDetection("CLEAN_RACE", p.playerId, at, "Terminó en el podio sin choques")
    }