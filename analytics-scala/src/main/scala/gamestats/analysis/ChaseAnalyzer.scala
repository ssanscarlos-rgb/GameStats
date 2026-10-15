package gamestats.analysis

import gamestats.model.*
import gamestats.analysis.Common.*

/** La Buena vs La Mala (ver docs/CONTRACT.md, secciones 2.4 y 5.3).
 *
 * Eventos: ROUTE_COMPUTED {found, optimalSteps, routeTurns}, PLAYER_MOVED, OBSTACLE_HIT {livesLeft},
 * TARGET_REACHED {steps, turns}, MATCH_FINISHED {winnerId}.
 */
object ChaseAnalyzer extends GameAnalyzer:
  val gameId = "chase"

  override def derive(c: Map[String, Double]): Map[String, Double] =
    val routes  = c.getOrElse("routeFound", 0.0)
    val reached = c.getOrElse("reached", 0.0)
    val steps   = c.getOrElse("reachedSteps", 0.0)
    Map(
      "successRate"      -> (if routes == 0 then 0.0 else reached / routes),
      "efficiency"       -> (if steps == 0 then 0.0 else c.getOrElse("reachedOptimalSteps", 0.0) / steps),
      "extraTurnsPerWin" -> (if reached == 0 then 0.0
      else (c.getOrElse("reachedTurns", 0.0) - c.getOrElse("reachedRouteTurns", 0.0)) / reached),
      "hitsPerMatch"     -> (if routes == 0 then 0.0 else c.getOrElse("obstacleHits", 0.0) / routes)
    )

  def analyze(matchId: String, events: List[GameEvent]): MatchAnalysis =
    def ofType(t: String) = events.filter(_.`type` == t)

    val routes  = ofType("ROUTE_COMPUTED")
    val targets = ofType("TARGET_REACHED")
    val hits    = ofType("OBSTACLE_HIT")
    val moves   = countBy(ofType("PLAYER_MOVED"))(_.playerId)
    val hitsBy  = countBy(hits)(_.playerId)

    // El primer evento de un tipo para cada jugador (en chase hay uno solo por partida).
    def firstBy(evs: List[GameEvent]): Map[String, GameEvent] =
      evs.flatMap(e => e.playerId.map(_ -> e)).groupMapReduce(_._1)(_._2)((a, _) => a)
    val routeOf  = firstBy(routes)
    val targetOf = firstBy(targets)

    val playerIds = events.flatMap(_.playerId).distinct.sorted
    val players = playerIds.map { p =>
      val route  = routeOf.get(p)
      val target = targetOf.get(p)
      // Los counters "reached*" solo cuentan en partidas ganadas: así la eficiencia
      // compara pasos usados con pasos óptimos solo cuando el recorrido terminó.
      def onWin(value: Option[Double]): Double = if target.isDefined then value.getOrElse(0.0) else 0.0
      val counters = Map(
        "routeFound"          -> (if route.flatMap(flag(_, "found")).contains(true) then 1.0 else 0.0),
        "reached"             -> (if target.isDefined then 1.0 else 0.0),
        "moves"               -> moves.getOrElse(p, 0).toDouble,
        "obstacleHits"        -> hitsBy.getOrElse(p, 0).toDouble,
        "reachedSteps"        -> onWin(target.flatMap(_.num("steps"))),
        "reachedOptimalSteps" -> onWin(route.flatMap(_.num("optimalSteps"))),
        "reachedTurns"        -> onWin(target.flatMap(_.num("turns"))),
        "reachedRouteTurns"   -> onWin(route.flatMap(_.num("routeTurns")))
      )
      PlayerMatchStats(p, counters, derive(counters))
    }

    val winner       = events.find(_.`type` == "MATCH_FINISHED").flatMap(_.str("winnerId"))
    val (start, end) = bounds(events)

    MatchAnalysis(
      matchId, gameId, start, end, durationSeconds(start, end), winner,
      eventCounts(events), players,
      patterns = perfectRuns(players, targetOf) ++ closeCalls(hits, targetOf) ++ noRoutes(routes)
    )

  /** Lee un campo booleano de data (GameEvent solo trae str y num). */
  private def flag(e: GameEvent, key: String): Option[Boolean] =
    e.data.flatMap(_.hcursor.get[Boolean](key).toOption)

  /** PERFECT_RUN: alcanzó a La Mala con los pasos óptimos y sin choques. */
  private def perfectRuns(players: List[PlayerMatchStats], targetOf: Map[String, GameEvent]): List[PatternDetection] =
    players.collect {
      case p if p.counters("reached") == 1.0
        && p.counters("obstacleHits") == 0.0
        && p.counters("reachedSteps") == p.counters("reachedOptimalSteps") =>
        PatternDetection("PERFECT_RUN", p.playerId, targetOf.get(p.playerId).map(_.timestamp),
          s"Ruta perfecta: ${p.counters("reachedSteps").toInt} pasos, sin choques")
    }

  /** CLOSE_CALL: quedó con 1 sola vida (un OBSTACLE_HIT con livesLeft = 1) y DESPUÉS alcanzó a La Mala. */
  private def closeCalls(hits: List[GameEvent], targetOf: Map[String, GameEvent]): List[PatternDetection] =
    targetOf.toList.sortBy(_._1).flatMap { (p, target) =>
      val lastLife = hits.exists { h =>
        h.playerId.contains(p) && h.num("livesLeft").contains(1.0) && h.timestamp.isBefore(target.timestamp)
      }
      if lastLife then
        List(PatternDetection("CLOSE_CALL", p, Some(target.timestamp), "La alcanzó con una sola vida"))
      else Nil
    }

  /** NO_ROUTE: el tablero no tenía camino posible hasta La Mala. */
  private def noRoutes(routes: List[GameEvent]): List[PatternDetection] =
    routes.collect {
      case r if flag(r, "found").contains(false) && r.playerId.isDefined =>
        PatternDetection("NO_ROUTE", r.playerId.get, Some(r.timestamp), "No existía ruta hasta La Mala")
    }