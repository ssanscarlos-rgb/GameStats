# GameStats

- `analytics-scala/` — módulo de análisis (Scala 3 + sbt + Cask + circe). Jean
- `ingest-go/` — módulo de ingestión (Go). Andrés
- `samples/` — datos de ejemplo compartidos
- `docs/CONTRACT.md` — formato de eventos y API entre módulos

## Scala: ejecutar
```bash
cd analytics-scala
sbt test
sbt run                      # http://localhost:8081
curl -X POST localhost:8081/analyze -H 'Content-Type: application/json' \
     --data @../samples/combat_match.json
curl localhost:8081/results/matches
curl localhost:8081/results/players
```

## Scala: Docker
```bash
cd analytics-scala
docker build -t gamestats-analyzer .
docker run -p 8081:8081 gamestats-analyzer
```
