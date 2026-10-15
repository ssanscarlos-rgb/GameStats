package gamestats

import gamestats.analysis.{Analyzers, ChaseAnalyzer}
import gamestats.model.*
import io.circe.Json
import io.circe.syntax.*
import java.time.Instant

class ChaseAnalyzerSuite extends munit.FunSuite:

  private val SampleFile = "chase_match.json"

  private lazy val result: MatchAnalysis = Analyzers.analyze(Samples.load(SampleFile)).head

  private def sampleTest(name: String)(body: => Unit): Unit =
    test(name) {
      assume(Samples.available(SampleFile), s"no se encontró ${Samples.path(SampleFile)}")
      body
    }

  sampleTest("usa el analizador de chase, no el genérico") {
    assertEquals(result.gameId, "chase")
    assertEquals(result.winnerId, Some("nati"))
  }

  sampleTest("counters y derivadas de nati (resultado esperado del contrato, sección 5.3)") {
    val nati = result.players.find(_.playerId == "nati").getOrElse(fail("falta nati"))
    val c    = nati.counters
    assertEquals(c("routeFound"), 1.0)
    assertEquals(c("reached"), 1.0)
    assertEquals(c("moves"), 9.0)
    assertEquals(c("obstacleHits"), 1.0)
    assertEquals(c("reachedSteps"), 9.0)
    assertEquals(c("reachedOptimalSteps"), 7.0)
    assertEquals(c("reachedTurns"), 4.0)
    assertEquals(c("reachedRouteTurns"), 1.0)

    val d = nati.derived
    assertEqualsDouble(d("successRate"), 1.0, 1e-9)
    assertEqualsDouble(d("efficiency"), 7.0 / 9.0, 1e-9) // 0.7778
    assertEqualsDouble(d("extraTurnsPerWin"), 3.0, 1e-9)
    assertEqualsDouble(d("hitsPerMatch"), 1.0, 1e-9)
  }

  sampleTest("la partida de ejemplo no dispara patrones") {
    // 9 pasos contra 7 óptimos (no es PERFECT_RUN) y le quedaron 2 vidas (no es CLOSE_CALL).
    assertEquals(result.patterns, Nil)
  }

  // ---------- Partidas construidas a mano para cada patrón ----------

  private val t0 = Instant.parse("2026-10-14T10:00:00Z")
  private var n  = 0

  private def ev(sec: Long, tipo: String, player: Option[String], data: (String, Json)*): GameEvent =
    n += 1
    GameEvent(s"c$n", t0.plusSeconds(sec), "chase", None, "c1", player, tipo, None,
      if data.isEmpty then None else Some(Json.obj(data*)))

  private def route(sec: Long, found: Boolean, optimal: Int, turns: Int) =
    ev(sec, "ROUTE_COMPUTED", Some("ana"),
      "found" -> found.asJson, "optimalSteps" -> optimal.asJson, "routeTurns" -> turns.asJson)
  private def move(sec: Long)            = ev(sec, "PLAYER_MOVED", Some("ana"), "turn" -> false.asJson)
  private def hit(sec: Long, lives: Int) = ev(sec, "OBSTACLE_HIT", Some("ana"), "livesLeft" -> lives.asJson)
  private def reached(sec: Long, steps: Int, turns: Int) =
    ev(sec, "TARGET_REACHED", Some("ana"), "steps" -> steps.asJson, "turns" -> turns.asJson)

  private def rulesOf(events: List[GameEvent]): List[String] =
    ChaseAnalyzer.analyze("c1", events).patterns.map(_.rule)

  test("PERFECT_RUN: pasos óptimos y sin choques") {
    val events = List(route(1, true, 3, 0), move(2), move(3), move(4), reached(4, 3, 0))
    assertEquals(rulesOf(events), List("PERFECT_RUN"))
  }

  test("pasos óptimos pero con un choque: no es PERFECT_RUN") {
    val events = List(route(1, true, 3, 0), move(2), hit(3, 2), move(4), move(5), reached(5, 3, 0))
    assert(!rulesOf(events).contains("PERFECT_RUN"))
  }

  test("CLOSE_CALL: la alcanzó después de quedar con una sola vida") {
    val events = List(route(1, true, 2, 0), hit(2, 2), hit(3, 1), move(4), move(5), reached(5, 4, 1))
    assertEquals(rulesOf(events), List("CLOSE_CALL"))
  }

  test("quedó con una vida pero NO la alcanzó: no es CLOSE_CALL") {
    val events = List(route(1, true, 2, 0), hit(2, 2), hit(3, 1), hit(4, 0))
    assert(!rulesOf(events).contains("CLOSE_CALL"))
  }

  test("NO_ROUTE: el tablero no tenía camino; todo queda en 0 sin dividir entre cero") {
    val r = ChaseAnalyzer.analyze("c1", List(route(1, false, 0, 0)))
    assertEquals(r.patterns.map(_.rule), List("NO_ROUTE"))
    val ana = r.players.head
    assertEquals(ana.counters("routeFound"), 0.0)
    assertEquals(ana.counters("reached"), 0.0)
    assertEquals(ana.derived.values.toList.distinct, List(0.0))
  }