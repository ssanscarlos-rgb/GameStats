# GameStats

Plataforma para recibir, procesar y analizar eventos de partidas de videojuegos.
Proyecto 1 del curso Lenguajes de Programación (TEC).

- **Go** (`ingest-go/`): recibe los eventos, los valida, los agrupa por partida con
  goroutines y canales, y entrega cada partida completa al módulo de análisis.
  Aquí también están el **simulador** (la fuente de eventos) y el **panel web**.
- **Scala** (`analytics-scala/`): analiza cada partida con colecciones y
  funciones (`filter`, `map`, `groupBy`, `foldLeft`, `collect`...), calcula
  estadísticas, detecta patrones y expone los resultados en JSON.

Juegos soportados: **combat** (disparos), **racing** (carrera por vueltas) y
**chase** (La Buena vs La Mala, persecución en un tablero).

```
 simulador ──POST /events──► ingest (Go, :8080) ──POST /analyze──► analyzer (Scala, :8081)
                               │ valida, agrupa                      │ analiza, guarda
                               ▼                                     ▼
                       GET /status, /rejected               GET /results/...
                               └──────────────► panel web (Go, :8090) ◄──┘
```

---

## 1. Ejecutar el sistema completo

### Requisitos

- **Docker** con **Compose v2** (Docker Desktop en Windows/macOS ya lo trae).
  Se comprueba con `docker compose version`.
- Conexión a internet la **primera vez** (descarga imágenes base y dependencias).
- Los puertos **8080, 8081 y 8090** libres.

No hace falta tener Go, Scala, Java ni sbt instalados: todo se compila dentro de Docker.

### Tres comandos

```bash
git clone https://github.com/ssanscarlos-rgb/GameStats.git
cd GameStats
docker compose up -d --build
```

La **primera** construcción tarda varios minutos (sobre todo la de Scala, que
descarga sbt y las librerías y corre las pruebas). Las siguientes son mucho más rápidas.

Cuando termina, comprobar que los tres servicios están arriba:

```bash
docker compose ps
```

`analyzer` e `ingest` deben decir `healthy` y `dashboard` debe decir `running`/`Up`.

Luego abrir **http://localhost:8090** en el navegador.

### Apagar

```bash
docker compose down
```

Los resultados se guardan en memoria: al apagar (o reiniciar) los servicios se
borran y el sistema arranca vacío.

---

## 2. Qué revisar (demostración guiada)

Todo se puede ver desde el panel en **http://localhost:8090**.

1. **Simulador** → dejar el modo **Una tanda**, los valores por defecto, y
   pulsar **Iniciar simulación**. El panel arranca el simulador, que juega en
   paralelo partidas de los tres juegos y luego manda casos especiales
   (eventos inválidos, duplicados, uno tardío y uno de otro juego).
2. **La ruta de un evento** (arriba): los contadores muestran el recorrido
   *Recibidos por Go → Partidas abiertas → Enviados a Scala → Partidas
   analizadas → Patrones detectados*, actualizándose en vivo.
3. **Eventos por segundo**: la gráfica muestra el flujo mientras corre la
   simulación y se congela cuando termina, para poder leer las cantidades.
   Debajo, **Salida del simulador** muestra lo que va imprimiendo el proceso.
4. **Resultados por juego**: pestañas *combat*, *racing* y *chase*, cada una con
   las estadísticas por jugador (sumando todas las partidas) y los patrones de ese juego.
5. **Partidas analizadas**: el análisis de cada partida (duración, ganador,
   jugadores, patrones). **Patrones detectados**: la lista de reglas que se
   cumplieron (`KILL_STREAK`, `COMEBACK`, `FASTEST_LAP`, `CLEAN_RACE`,
   `PERFECT_RUN`, `CLOSE_CALL`, `NO_ROUTE`).
6. **Eventos rechazados**: cada evento descartado con el motivo exacto.

Una partida de racing del simulador se corta a propósito sin `MATCH_FINISHED`
(*racing-…-abandonada*). Go la da por terminada tras **30 s sin eventos** y
entonces aparece en los resultados: es normal ver un último aumento de
partidas analizadas unos segundos después de que termina la simulación.

El modo **Continuo** juega partidas nuevas sin parar durante el tiempo elegido
(30 s a 10 min), para ver el sistema bajo carga sostenida.

### Lo mismo sin el panel

```bash
docker compose run --rm simulator -seed 42      # una tanda; misma semilla = mismas partidas
curl http://localhost:8080/status               # contadores del ingest (Go)
curl http://localhost:8080/rejected             # eventos rechazados y por qué
curl http://localhost:8081/results/matches      # análisis de cada partida (Scala)
curl http://localhost:8081/results/players      # estadísticas por jugador (Scala)
```

En Windows PowerShell se usa `curl.exe` en lugar de `curl`.

También se puede analizar una partida de ejemplo directamente en Scala:

```bash
curl -X POST http://localhost:8081/analyze -H "Content-Type: application/json" \
     --data @samples/combat_match.json
curl http://localhost:8081/results/match/combat-001
```

Los resultados esperados de los tres ejemplos de `samples/` están escritos en
[`docs/CONTRACT.md`](docs/CONTRACT.md), sección 5.

---

## 3. Dónde está cada requisito del enunciado

### Funcionalidades mínimas (sección 9)

| # | Requisito | Dónde |
|---|---|---|
| 1 | Registrar o recibir eventos | `POST /events` del ingest: `ingest-go/internal/api/server.go` |
| 2 | Validar y procesar eventos | `ingest-go/internal/event/event.go` (`Validate`); reglas con estado (duplicados, tardíos, juego equivocado) en `ingest-go/internal/pipeline/worker.go` |
| 3 | Identificar juegos, partidas y jugadores | Go agrupa por `matchId` y lleva los jugadores de cada partida (`worker.go`); Scala agrupa por `(gameId, matchId)` y elige el analizador por `gameId` (`analysis/GameAnalyzer.scala`) |
| 4 | Procesar varios eventos de una misma partida | Go junta todos los eventos de la partida y la envía completa (`pipeline.go`); Scala la ordena por `timestamp` y la analiza entera |
| 5 | Estadísticas definidas por el grupo | `analytics-scala/src/main/scala/gamestats/analysis/` (`CombatAnalyzer`, `RacingAnalyzer`, `ChaseAnalyzer`); lista completa en `docs/CONTRACT.md` §5 |
| 6 | Estadística agregada por jugador | `GET /results/players` (`ResultStore.playerSummaries`): K/D, tiempo promedio por vuelta, tasa de éxito... sumando todas las partidas |
| 7 | Estadística agregada por partida | `GET /results/matches`: duración, ganador, conteo por tipo de evento; en racing además la vuelta más rápida |
| 8 | Estadística que combina tipos de eventos | combat: precisión = `DAMAGE_DEALT` / `SHOT_FIRED`; racing: tasa de choques = `CRASH` / `LAP_COMPLETED`; chase: eficiencia = pasos óptimos de `ROUTE_COMPUTED` / pasos de `TARGET_REACHED` |
| 9 | Regla o patrón sobre una secuencia de eventos | `KILL_STREAK` y `COMEBACK` (combat), `COMEBACK` y `CLEAN_RACE` (racing, reconstruye la posición vuelta a vuelta), `PERFECT_RUN` y `CLOSE_CALL` (chase) |
| 10 | Consultar o presentar resultados | API JSON de Scala (`/results/...`) y panel web en http://localhost:8090 |

### Desarrollo, versionamiento y deployment (sección 10)

| Requisito | Dónde |
|---|---|
| Repositorio Git con historial | Este repositorio (pestaña *Commits*) |
| Construcción reproducible | `ingest-go/go.mod` (Go 1.24, sin dependencias externas); `analytics-scala/build.sbt` con versiones fijas; Dockerfiles multi-etapa con imágenes base fijadas |
| Contenerización con Docker | `ingest-go/Dockerfile` (ingest, simulador y panel en una imagen), `analytics-scala/Dockerfile` |
| Ejecución integrada | `docker-compose.yml`: red interna, healthchecks, orden de arranque (`depends_on: service_healthy`) |
| Pipeline CI/CD | `.github/workflows/` (ver sección 7 de este README) |
| Deployment | Sección 8 de este README |

### Entregables (sección 11)

| Entregable | Dónde |
|---|---|
| Código fuente Go y Scala | `ingest-go/`, `analytics-scala/` |
| Especificación del formato de eventos | [`docs/CONTRACT.md`](docs/CONTRACT.md): §2 eventos, §6 cómo enviarlos (API de Go), §3–4 API y resultados de Scala |
| Juegos considerados y justificación de los eventos | [`docs/DISENO.md`](docs/DISENO.md) §1 |
| Estadísticas y reglas | [`docs/CONTRACT.md`](docs/CONTRACT.md) §5 (con resultados esperados) |
| Dockerfiles y configuración | `ingest-go/Dockerfile`, `analytics-scala/Dockerfile`, `docker-compose.yml` |
| Configuración del pipeline CI/CD | `.github/workflows/*.yml` |
| Instalación, ejecución y deployment | Este README |
| Demostración funcional | Sección 2 de este README (panel + simulador) |

Las decisiones de arquitectura y la comparación entre el enfoque imperativo
(Go) y el funcional (Scala) están en [`docs/DISENO.md`](docs/DISENO.md).

---

## 4. Estructura del repositorio

```
GameStats/
├── docker-compose.yml            sistema completo
├── docs/
│   ├── CONTRACT.md               formato de eventos, API, estadísticas y reglas
│   └── DISENO.md                 juegos, arquitectura y justificación de decisiones
├── samples/                      una partida de ejemplo por juego (con resultados esperados)
├── ingest-go/                    módulo Go
│   ├── Dockerfile
│   ├── cmd/
│   │   ├── ingest/               servicio de ingestión (:8080)
│   │   ├── simulator/            fuente de eventos simulada
│   │   └── dashboard/            panel web (:8090); la página está en web/index.html
│   └── internal/
│       ├── event/                modelo de evento y validación
│       ├── api/                  servidor HTTP (POST /events, /status, /rejected)
│       ├── pipeline/             trabajadores y mensajeros (goroutines + canales)
│       └── analyzer/             cliente HTTP hacia Scala, con reintentos
├── analytics-scala/              módulo Scala
│   ├── Dockerfile
│   ├── build.sbt
│   └── src/
│       ├── main/scala/gamestats/
│       │   ├── Main.scala        servidor HTTP (Cask)
│       │   ├── ResultStore.scala resultados en memoria y agregado por jugador
│       │   ├── model/            tipos de datos y JSON (circe)
│       │   └── analysis/         un analizador por juego + genérico
│       └── test/scala/gamestats/ pruebas (munit)
└── .github/workflows/            CI/CD
```

---

## 5. Ejecutar sin Docker (desarrollo)

Requiere **Go 1.24+**, **JDK 17** y **sbt 1.10+**. Cada servicio en su propia terminal:

```bash
# 1) Scala (análisis), queda en http://localhost:8081
cd analytics-scala
sbt run

# 2) Go: ingest, queda en http://localhost:8080
cd ingest-go
go run ./cmd/ingest

# 3) Go: panel, queda en http://localhost:8090
cd ingest-go
go run ./cmd/dashboard
```

Los comandos de Go se corren **dentro de `ingest-go/`** (ahí está `go.mod`).
Desde el panel se lanza el simulador; si no encuentra el binario, lo compila
solo con `go build`. También se puede correr a mano:

```bash
cd ingest-go
go run ./cmd/simulator                        # una tanda: 3 combat, 3 chase, 2 racing + casos especiales
go run ./cmd/simulator -combat 5 -delay 0ms   # más partidas, sin pausas
go run ./cmd/simulator -duration 5m           # modo continuo durante 5 minutos
go run ./cmd/simulator -out ../samples/sim    # escribir las partidas como JSON en vez de enviarlas
```

Opciones del simulador:

| Opción | Por defecto | Qué hace |
|---|---|---|
| `-url` | `$INGEST_URL` o `http://localhost:8080` | dirección del ingest |
| `-combat`, `-chase`, `-racing` | 3, 3, 2 | partidas al azar de cada juego |
| `-special` | `true` | partidas guionizadas (remontadas, carrera perfecta, sin ruta, carrera abandonada) y casos especiales |
| `-delay` | `30ms` | pausa promedio entre eventos de una partida |
| `-seed` | `0` (al azar) | semilla: la misma semilla genera las mismas partidas |
| `-duration` | `0` (una tanda) | modo continuo: jugar partidas nuevas durante ese tiempo |
| `-parallel` | `6` | modo continuo: partidas en curso a la vez |
| `-out` | – | carpeta donde escribir las partidas en JSON |

---

## 6. Configuración y API

### Variables de entorno

| Servicio | Variable | Por defecto | Uso |
|---|---|---|---|
| analyzer | `PORT` | `8081` | puerto HTTP |
| ingest | `PORT` | `8080` | puerto HTTP |
| ingest | `ANALYZER_URL` | `http://localhost:8081` | dirección de Scala (en Compose: `http://analyzer:8081`) |
| ingest | `WORKERS` | `4` | trabajadores que agrupan partidas |
| ingest | `SENDERS` | `2` | mensajeros que envían partidas a Scala |
| ingest | `IDLE_TIMEOUT` | `30s` | tiempo sin eventos para dar una partida por terminada |
| dashboard | `PORT` | `8090` | puerto HTTP |
| dashboard | `INGEST_URL` | `http://localhost:8080` | dirección del ingest |
| dashboard | `ANALYZER_URL` | `http://localhost:8081` | dirección de Scala |
| dashboard | `SIMULATOR_BIN` | – | ruta del simulador; si falta, busca `simulator` junto al panel y si no, lo compila |

Con Compose, `IDLE_TIMEOUT` se puede cambiar al arrancar:
`IDLE_TIMEOUT=10s docker compose up -d` (macOS/Linux).

### Ingest (Go, puerto 8080)

| Método y ruta | Qué hace |
|---|---|
| `POST /events` | Recibe un evento, un arreglo de eventos o `{"events":[...]}`. Valida cada uno por separado y responde `202` con `{"accepted","rejected","errors"}` |
| `GET /status` | Contadores: recibidos, rechazados, duplicados, tardíos, partidas abiertas, enviadas, fallidas |
| `GET /rejected` | Últimos eventos rechazados con el motivo y el JSON original |
| `GET /health` | `200` si el servicio está arriba |

### Analyzer (Scala, puerto 8081)

| Método y ruta | Qué hace |
|---|---|
| `POST /analyze` | Recibe `{"events":[...]}` con una o varias partidas y las analiza |
| `GET /results/matches` | Análisis de todas las partidas |
| `GET /results/match/{matchId}` | Análisis de una partida (`404` si no existe) |
| `GET /results/players` | Estadísticas por jugador y juego, sumando todas sus partidas |
| `GET /health` | `{"status":"ok"}` |

El formato de cada evento y de cada respuesta está en [`docs/CONTRACT.md`](docs/CONTRACT.md).

### Panel (Go, puerto 8090)

Sirve la página y reenvía sus consultas: `/api/ingest/...` va al ingest y
`/api/analyzer/...` a Scala (solo lectura). `GET /api/sim`, `POST /api/sim/start`
y `POST /api/sim/stop` controlan el simulador.

---

## 7. Pruebas y CI/CD

### Pruebas locales

```bash
cd ingest-go && go test -race ./...     # Go, con el detector de condiciones de carrera
cd analytics-scala && sbt test          # Scala (munit)
```

Las pruebas de Scala usan partidas armadas en el propio código y, para racing
y chase, también las de `samples/`, comparando con los resultados esperados de
`docs/CONTRACT.md`. Las pruebas de Go cubren la validación, el pipeline
concurrente (duplicados, tardíos, timeout, reenvío tras un fallo), el cliente
con reintentos, el simulador y el panel.

### GitHub Actions (`.github/workflows/`)

| Workflow | Cuándo corre | Qué hace |
|---|---|---|
| `go-ci.yml` (**Go CI**) | cambios en `ingest-go/` o `samples/` | `gofmt`, `go vet`, pruebas con `-race`, compila los tres programas y construye la imagen Docker |
| `scala-ci.yml` (**Scala CI**) | cambios en `analytics-scala/` o `samples/` | `sbt test`, empaqueta el jar y construye la imagen Docker |
| `system-ci.yml` (**Sistema completo**) | cada push a `main` y cada pull request | levanta todo con `docker compose up --build --wait`, corre el simulador con semilla fija, comprueba que las 14 partidas recorrieron simulador → Go → Scala, que los tres analizadores y sus patrones funcionaron, que Go rechazó los casos especiales y que el panel responde; al final apaga los contenedores |
| `release.yml` (**Release**) | al publicar un release en GitHub (ej. `v1.0`) | construye las dos imágenes y las publica en el registro de GitHub (`ghcr.io`) con la versión como etiqueta; el build de Scala corre las pruebas, así que una versión con pruebas rotas no se publica |

Los resultados se ven en la pestaña **Actions** del repositorio. El flujo completo es:
CI en cada push → `main` en verde → release `vX.Y` → imágenes versionadas en **Packages**.

---

## 8. Deployment

El entorno de deployment definido por el equipo es **una máquina con Docker**
(la de cualquier integrante o la del evaluador). Desplegar una versión es:

```bash
git pull                          # o git clone la primera vez
docker compose up -d --build      # construye la versión nueva y reemplaza los contenedores
```

- `-d` deja los servicios corriendo en segundo plano.
- `restart: unless-stopped` los vuelve a levantar si se caen o si se reinicia Docker.
- Los healthchecks hacen que `ingest` espere a que Scala responda y que el
  panel espere al ingest.

Para desplegar una versión anterior: `git checkout v1.0` (o cualquier commit) y el mismo
`docker compose up -d --build`.

### Desplegar desde las imágenes publicadas (sin compilar)

Cada release publica sus imágenes en `ghcr.io` (sección **Packages** del repositorio).
Para desplegar esa versión sin compilar nada, desde la carpeta del repositorio:

```bash
V=v1.0
docker pull ghcr.io/ssanscarlos-rgb/gamestats-go:$V
docker pull ghcr.io/ssanscarlos-rgb/gamestats-analyzer:$V
docker tag ghcr.io/ssanscarlos-rgb/gamestats-go:$V gamestats-go
docker tag ghcr.io/ssanscarlos-rgb/gamestats-analyzer:$V gamestats-analyzer
docker compose up -d              # sin --build: usa las imágenes descargadas
```

Las imágenes son para `linux/amd64`. En una Mac con chip Apple, Docker Desktop
las corre con emulación (funciona, pero arranca más lento); ahí conviene el
camino normal con `--build`.

La misma secuencia es la que ejecuta el workflow **Sistema completo** en una
máquina limpia de GitHub en cada push a `main`: si ese workflow está en verde,
esa versión se despliega con estos comandos.

---

## 9. Problemas comunes

| Síntoma | Solución |
|---|---|
| `port is already allocated` / `address already in use` | Algo usa 8080, 8081 u 8090 (por ejemplo, una ejecución con `go run` o `sbt run`). Cerrarlo. En macOS/Linux: `lsof -ti :8080,8081,8090 \| xargs kill` |
| `docker compose` no existe | Docker viejo con Compose v1: usar `docker-compose` (con guion) o actualizar Docker Desktop |
| La primera construcción tarda mucho | Normal: Scala descarga sbt, librerías y corre pruebas. Ver avance con `docker compose up --build` (sin `-d`) |
| El panel dice "sin conexión" con Go o con Scala | `docker compose ps` y `docker compose logs analyzer` (o `ingest`) |
| Quedan resultados de una corrida anterior | `docker compose restart analyzer ingest` deja todo en cero |
| Una partida aparece en los resultados unos segundos después | Es una partida sin `MATCH_FINISHED`: se envía tras `IDLE_TIMEOUT` (30 s) |
| Ver qué está haciendo un servicio | `docker compose logs -f ingest` |

---

## Autores

- Jean — módulo Scala, contrato Go ↔ Scala
- Andrés — módulo Go (ingest, simulador, panel), Docker y CI/CD
