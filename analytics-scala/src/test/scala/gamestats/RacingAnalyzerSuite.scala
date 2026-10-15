package gamestats

import gamestats.analysis.{Analyzers, RacingAnalyzer}
import gamestats.model.*
import io.circe.Json
import io.circe.syntax.*
import java.time.Instant

class RacingAnalyzerSuite extends munit.FunSuite:

  private val SampleFile = "racing_match.json"

  // lazy: solo se lee el archivo cuando una prueba lo necesita.
  // Pasa por Analyzers, así también se prueba que racing está registrado.
  private lazy val result: MatchAnalysis = Analyzers.analyze(Samples.load(SampleFile)).head

  /** Una prueba que usa samples/: se salta si la carpeta no está (p. ej. en docker build). */
  private def sampleTest(name: String)(body: => Unit): Unit =
    test(name) {
      assume(Samples.available(SampleFile), s"no se encontró ${Samples.path(SampleFile)}")
      body
    }

  private def player(id: String): PlayerMatchStats =
    result.players.find(_.playerId == id).getOrElse(fail(s"falta el piloto $id"))

  private def rules: List[(String, String)] = result.patterns.map(p => p.rule -> p.playerId)

  sampleTest("usa el analizador de racing, no el genérico") {
    assertEquals(result.gameId, "racing")
    assert(player("vale").counters.contains("lapsCompleted"))
    assertEquals(result.winnerId, Some("vale"))
  }

  sampleTest("counters de vale (resultado esperado del contrato, sección 5.2)") {
    val c = player("vale").counters
    assertEquals(c("lapsCompleted"), 4.0)
    assertEquals(c("totalTimeMs"), 244335.0)
    assertEquals(c("crashes"), 1.0)
    assertEquals(c("timeLostMs"), 12000.0)
    assertEquals(c("podiums"), 1.0)
    assertEquals(c("positionsGained"), 3.0) // 4.º tras la vuelta 1, 1.º al final
  }

  sampleTest("derivadas de vale") {
    val d = player("vale").derived
    assertEqualsDouble(d("avgLapMs"), 61083.75, 1e-6)
    assertEqualsDouble(d("consistencyMs"), 5460.9, 0.1)
    assertEqualsDouble(d("crashRate"), 0.25, 1e-9)
    assertEqualsDouble(d("timeLostPct"), 0.0491, 1e-4)
  }

  sampleTest("podios, posiciones ganadas y consistencia del resto") {
    val esperado = List(
      ("luis", 1.0, -1.0, 362.7),
      ("sofi", 1.0, -1.0, 248.7),
      ("leo",  0.0, -1.0, 107.4)
    )
    esperado.foreach { (id, podios, ganadas, consistencia) =>
      val p = player(id)
      assertEquals(p.counters("podiums"), podios, id)
      assertEquals(p.counters("positionsGained"), ganadas, id)
      assertEqualsDouble(p.derived("consistencyMs"), consistencia, 0.1)
    }
  }

  sampleTest("patrones: COMEBACK y FASTEST_LAP de vale, CLEAN_RACE de luis y sofi") {
    assert(rules.contains("COMEBACK" -> "vale"))
    assert(rules.contains("FASTEST_LAP" -> "vale"))
    assert(rules.contains("CLEAN_RACE" -> "luis"))
    assert(rules.contains("CLEAN_RACE" -> "sofi"))
    assert(!rules.contains("CLEAN_RACE" -> "vale"), "vale chocó")
    assert(!rules.contains("CLEAN_RACE" -> "leo"), "leo no fue al podio")
  }

  test("la consistencia se puede recalcular con counters sumados de varias carreras") {
    // Dos vueltas de 100 ms: promedio 100, sin variación.
    val iguales = RacingAnalyzer.derive(Map("lapsCompleted" -> 2.0, "totalTimeMs" -> 200.0, "lapTimeSqSum" -> 20000.0))
    assertEqualsDouble(iguales("consistencyMs"), 0.0, 1e-9)
    // Vueltas de 90 y 110: desviación estándar 10.
    val distintas = RacingAnalyzer.derive(Map("lapsCompleted" -> 2.0, "totalTimeMs" -> 200.0, "lapTimeSqSum" -> 20200.0))
    assertEqualsDouble(distintas("consistencyMs"), 10.0, 1e-9)
  }

  test("sin vueltas, las derivadas valen 0 (no se divide entre cero)") {
    val d = RacingAnalyzer.derive(Map.empty)
    assertEquals(d.values.toList.distinct, List(0.0))
  }

  test("carrera abandonada (sin MATCH_FINISHED): sin podios ni CLEAN_RACE") {
    val t0 = Instant.parse("2026-10-14T10:00:00Z")
    def lap(i: Int, sec: Long, p: String, n: Int, ms: Int) =
      GameEvent(s"r$i", t0.plusSeconds(sec), "racing", None, "r1", Some(p), "LAP_COMPLETED", None,
        Some(Json.obj("lap" -> n.asJson, "lapTimeMs" -> ms.asJson)))
    val events = List(lap(1, 60, "ana", 1, 60000), lap(2, 62, "luis", 1, 62000))
    val r = RacingAnalyzer.analyze("r1", events)
    assert(r.players.forall(_.counters("podiums") == 0.0))
    assert(!r.patterns.exists(_.rule == "CLEAN_RACE"))
    assertEquals(r.winnerId, None)
  }