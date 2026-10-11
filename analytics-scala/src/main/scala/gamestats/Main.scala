package gamestats

import gamestats.analysis.Analyzers
import gamestats.model.*
import io.circe.*
import io.circe.parser.decode
import io.circe.syntax.*

object Main extends cask.MainRoutes:
  override def host: String = "0.0.0.0"
  override def port: Int    = sys.env.get("PORT").flatMap(_.toIntOption).getOrElse(8081)

  private val store = ResultStore()

  private def json[A: Encoder](a: A, status: Int = 200): cask.Response[String] =
    cask.Response(a.asJson.noSpaces, status, Seq("Content-Type" -> "application/json"))

  @cask.get("/health")
  def health() = json(Json.obj("status" -> "ok".asJson))

  /** Go envía {"events":[...]} (eventos de una o varias partidas). */
  @cask.post("/analyze")
  def analyze(request: cask.Request) =
    decode[AnalyzeRequest](request.text()) match
      case Left(err) => json(Json.obj("error" -> err.getMessage.asJson), 400)
      case Right(req) =>
        val results = Analyzers.analyze(req.events)
        store.save(results)
        json(Json.obj("analyzedMatches" -> results.map(_.matchId).asJson))

  @cask.get("/results/matches")
  def matches() = json(store.allMatches)

  @cask.get("/results/match", subpath = true)
  def matchById(request: cask.Request) =
    request.remainingPathSegments.headOption.flatMap(store.findMatch) match
      case Some(m) => json(m)
      case None    => json(Json.obj("error" -> "match not found".asJson), 404)

  @cask.get("/results/players")
  def players() = json(store.playerSummaries)

  initialize()
