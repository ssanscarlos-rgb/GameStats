# --- Build ---
# Verifica que este tag exista en https://hub.docker.com/r/sbtscala/scala-sbt/tags (JDK 17 + sbt 1.10.x)
FROM sbtscala/scala-sbt:eclipse-temurin-17.0.13_11_1.10.7_3.3.4 AS build
WORKDIR /app
COPY project project
COPY build.sbt .
RUN sbt update
COPY src src
RUN sbt test assembly

# --- Runtime ---
FROM eclipse-temurin:17-jre
WORKDIR /app
COPY --from=build /app/target/scala-3.3.4/gamestats-analyzer.jar .
ENV PORT=8081
EXPOSE 8081
CMD ["java", "-jar", "gamestats-analyzer.jar"]
