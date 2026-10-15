ThisBuild / scalaVersion := "3.3.4"
ThisBuild / version      := "0.1.0"

lazy val root = (project in file("."))
  .settings(
    name := "gamestats-analyzer",
    // Cask arranca el servidor en hilos de fondo: sin fork, `sbt run` lo cierra enseguida.
    run / fork := true,
    libraryDependencies ++= Seq(
      "com.lihaoyi"   %% "cask"         % "0.9.4",
      "io.circe"      %% "circe-core"   % "0.14.10",
      "io.circe"      %% "circe-parser" % "0.14.10",
      "org.scalameta" %% "munit"        % "1.0.2" % Test
    ),
    assembly / mainClass       := Some("gamestats.Main"),
    assembly / assemblyJarName := "gamestats-analyzer.jar",
    assembly / assemblyMergeStrategy := {
      case PathList("META-INF", "services", _*) => MergeStrategy.concat
      case PathList("META-INF", _*)             => MergeStrategy.discard
      case _                                    => MergeStrategy.first
    }
  )