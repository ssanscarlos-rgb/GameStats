package gamestats

import gamestats.analysis.CombatAnalyzer
import gamestats.model.*
import io.circe.Json
import java.time.Instant

class CombatAnalyzerSuite extends munit.FunSuite:
  private val t0 = Instant.parse("2026-09-01T10:00:00Z")

  private def elim(i: Int, sec: Long, killer: String, victim: String) =
    GameEvent(s"e$i", t0.plusSeconds(sec), "combat", None, "m1", Some(killer),
      "PLAYER_ELIMINATED", None, Some(Json.obj("victimId" -> Json.fromString(victim))))

  private def finished(sec: Long, winner: String) =
    GameEvent("fin", t0.plusSeconds(sec), "combat", None, "m1", None,
      "MATCH_FINISHED", None, Some(Json.obj("winnerId" -> Json.fromString(winner))))

  private val events = List(
    elim(1, 10, "bob", "alice"), elim(2, 15, "bob", "alice"), elim(3, 20, "bob", "alice"),
    elim(4, 60, "alice", "bob"), elim(5, 90, "alice", "bob"), elim(6, 120, "alice", "bob"),
    elim(7, 150, "alice", "bob"), finished(160, "alice")
  )

  test("kills, deaths y K/D por jugador") {
    val r     = CombatAnalyzer.analyze("m1", events)
    val alice = r.players.find(_.playerId == "alice").get
    assertEquals(alice.counters("kills"), 4.0)
    assertEquals(alice.counters("deaths"), 3.0)
    assertEqualsDouble(alice.derived("kd"), 4.0 / 3.0, 1e-9)
  }

  test("detecta racha de bob y remontada de alice") {
    val rules = CombatAnalyzer.analyze("m1", events).patterns.map(p => p.rule -> p.playerId)
    assert(rules.contains("KILL_STREAK" -> "bob"))
    assert(rules.contains("COMEBACK" -> "alice"))
  }
