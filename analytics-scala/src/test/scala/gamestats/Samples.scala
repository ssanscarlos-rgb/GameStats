package gamestats

import gamestats.model.*
import io.circe.parser.decode
import java.nio.file.{Files, Path, Paths}

/** Lee los ejemplos compartidos con Go (carpeta samples/ en la raíz del repo).
 *
 * sbt e IntelliJ ejecutan las pruebas desde analytics-scala/, así que la ruta es ../samples.
 * Dentro de `docker build` solo se copia analytics-scala/: ahí la carpeta no existe y las
 * pruebas que la usan se saltan (ver `sampleTest` en cada suite) en lugar de fallar.
 */
object Samples:
  def path(file: String): Path = Paths.get("..", "samples", file)

  def available(file: String): Boolean = Files.exists(path(file))

  def load(file: String): List[GameEvent] =
    decode[AnalyzeRequest](Files.readString(path(file))) match
      case Right(req) => req.events
      case Left(err)  => throw err // io.circe.Error es una excepción
 