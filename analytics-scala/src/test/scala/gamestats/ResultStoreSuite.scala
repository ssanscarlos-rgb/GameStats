package gamestats

import gamestats.model.*
import java.time.Instant

class ResultStoreSuite extends munit.FunSuite:

  private val t0 = Instant.parse("2026-11-18T10:00:00Z")

  private def chasePlayer(id: String, reached: Boolean): PlayerMatchStats =
    PlayerMatchStats(
      id,
      Map(
        "routeFound" -> 1.0, "reached" -> (if reached then 1.0 else 0.0), "moves" -> 8.0,
        "obstacleHits" -> 0.0,
        "reachedSteps" -> (if reached then 8.0 else 0.0),
        "reachedOptimalSteps" -> (if reached then 8.0 else 0.0),
        "reachedTurns" -> 0.0, "reachedRouteTurns" -> 0.0
      ),
      Map.empty
    )

  private def chaseMatch(id: String, winner: Option[String], seconds: Long): MatchAnalysis =
    MatchAnalysis(
      id, "chase", Some(t0), Some(t0.plusSeconds(seconds)), seconds, winner,
      Map("MATCH_STARTED" -> 1, "PLAYER_MOVED" -> 16, "MATCH_FINISHED" -> 1),
      List(chasePlayer("ana", winner.contains("ana")), chasePlayer("cpu", winner.contains("cpu"))),
      List(PatternDetection("PERFECT_RUN", winner.getOrElse("ana"), Some(t0), "ruta perfecta"))
    )

  private def combatMatch(id: String): MatchAnalysis =
    MatchAnalysis(
      id, "combat", Some(t0), Some(t0.plusSeconds(60)), 60, Some("ana"),
      Map("MATCH_STARTED" -> 1, "SHOT_FIRED" -> 4, "MATCH_FINISHED" -> 1),
      List(
        PlayerMatchStats("ana", Map("kills" -> 2.0, "deaths" -> 1.0, "shotsFired" -> 3.0, "shotsHit" -> 2.0, "damageDealt" -> 200.0), Map.empty),
        PlayerMatchStats("bot1", Map("kills" -> 1.0, "deaths" -> 2.0, "shotsFired" -> 1.0, "shotsHit" -> 1.0, "damageDealt" -> 100.0), Map.empty)
      ),
      Nil
    )

  private def storeWithData(): ResultStore =
    val store = ResultStore()
    store.save(List(chaseMatch("chase-1", Some("ana"), 20), chaseMatch("chase-2", Some("cpu"), 40), combatMatch("combat-1")))
    store

  test("sin partidas: listas vacías y promedios en cero") {
    val o = ResultStore().overview
    assertEquals(o.games, Nil)
    assertEquals(o.total.matchesAnalyzed, 0)
    assertEquals(o.total.avgDurationSeconds, 0.0)
  }

  test("un resumen por juego, ordenado por gameId") {
    assertEquals(storeWithData().overview.games.map(_.gameId), List("chase", "combat"))
  }

  test("chase: cuenta partidas, ganadores, duración y jugadores") {
    val chase = storeWithData().overview.games.find(_.gameId == "chase").getOrElse(fail("falta chase"))
    assertEquals(chase.matchesAnalyzed, 2)
    assertEquals(chase.matchesWithWinner, 2)
    assertEquals(chase.playersCount, 2) // ana y cpu, aunque salgan en las dos partidas
    assertEquals(chase.totalDurationSeconds, 60L)
    assertEqualsDouble(chase.avgDurationSeconds, 30.0, 1e-9)
    assertEquals(chase.eventCounts("PLAYER_MOVED"), 32)
    assertEquals(chase.patternCounts, Map("PERFECT_RUN" -> 2))
  }

  test("chase: suma los counters y recalcula las derivadas con su analizador") {
    val chase = storeWithData().overview.games.find(_.gameId == "chase").getOrElse(fail("falta chase"))
    assertEquals(chase.counters("routeFound"), 4.0)
    assertEquals(chase.counters("reached"), 2.0)
    assertEqualsDouble(chase.derived("successRate"), 0.5, 1e-9) // 2 llegaron de 4 rutas
    assertEqualsDouble(chase.derived("efficiency"), 1.0, 1e-9)
  }

  test("combat: derivadas globales desde los counters sumados") {
    val combat = storeWithData().overview.games.find(_.gameId == "combat").getOrElse(fail("falta combat"))
    assertEquals(combat.counters("kills"), 3.0)
    assertEqualsDouble(combat.derived("accuracy"), 0.75, 1e-9) // 3 impactos de 4 disparos
  }

  test("totales generales: suman juegos y cuentan cada jugador una vez") {
    val t = storeWithData().overview.total
    assertEquals(t.gamesCount, 2)
    assertEquals(t.matchesAnalyzed, 3)
    assertEquals(t.matchesWithWinner, 3)
    assertEquals(t.playersCount, 3) // ana, cpu y bot1
    assertEquals(t.totalDurationSeconds, 120L)
    assertEqualsDouble(t.avgDurationSeconds, 40.0, 1e-9)
    assertEquals(t.eventCounts("MATCH_STARTED"), 3)
  }

  test("reenviar un matchId reemplaza el anterior y no duplica el conteo") {
    val store = storeWithData()
    store.save(List(chaseMatch("chase-1", Some("cpu"), 20)))
    assertEquals(store.overview.total.matchesAnalyzed, 3)
  }
