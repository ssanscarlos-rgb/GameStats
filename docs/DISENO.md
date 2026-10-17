# GameStats — Diseño y justificación

Este documento explica **por qué** el sistema está hecho como está: qué juegos se
eligieron y por qué esos eventos, cómo se reparte el trabajo entre Go y Scala, y
qué ganamos y perdimos con cada paradigma. El **qué** (formato exacto de eventos,
API, fórmulas y resultados esperados) está en [`CONTRACT.md`](CONTRACT.md); cómo
ejecutarlo está en el [`README`](../README.md).

---

## 1. Juegos considerados y eventos seleccionados

### 1.1 Criterio

Se eligieron **tres juegos de naturaleza distinta a propósito**, para comprobar
que el núcleo no depende de un juego:

| Juego | Naturaleza | Jugadores por partida | Qué lo hace interesante |
|---|---|---|---|
| `combat` | acción, enfrentamiento directo | 2 a 4 | las estadísticas salen de la interacción **entre** jugadores (quién elimina a quién) |
| `racing` | competencia contra el reloj | 3 a 6 | el estado importante (la posición) **no viene en ningún evento**: hay que reconstruirlo |
| `chase` | puzle sobre un tablero (La Buena vs La Mala) | 1 | se compara lo que el jugador hizo contra lo **óptimo** que calculó el juego |

`chase` se basa en un proyecto anterior de Jean (Java + Prolog), lo que da un
juego real como referencia para decidir qué eventos produciría.

Para los tres se siguió la misma regla: **el juego emite solo hechos que conoce
en el momento**, y todo lo que se pueda deducir lo calcula Scala. Así los
eventos son baratos de producir para un juego real y el análisis tiene trabajo
de verdad (no se limita a copiar números que ya venían calculados).

### 1.2 Eventos generales

`MATCH_STARTED`, `PLAYER_JOINED`, `PLAYER_LEFT` y `MATCH_FINISHED` son comunes a
todos los juegos. `MATCH_FINISHED` cumple dos papeles: le dice a Go que la
partida está completa (puede enviarla a analizar) y trae el `winnerId`, que
los patrones de remontada necesitan.

### 1.3 `combat`

| Evento | Por qué está |
|---|---|
| `SHOT_FIRED` | sin los disparos fallidos no se puede medir precisión |
| `DAMAGE_DEALT` | daño total y por impacto; un disparo que acierta produce daño |
| `PLAYER_ELIMINATED` | eliminaciones y muertes; la víctima va en `data.victimId` para que un solo evento cuente para los dos jugadores |

Con estos tres se obtiene: K/D, precisión (combina `SHOT_FIRED` y
`DAMAGE_DEALT`), daño promedio, y dos patrones que necesitan la **secuencia**
de eliminaciones: rachas (`KILL_STREAK`) y remontadas (`COMEBACK`).

### 1.4 `racing`

| Evento | Por qué está |
|---|---|
| `LAP_COMPLETED` con `lap` y `lapTimeMs` | es lo único que un juego de carreras sabe con certeza en cada vuelta |
| `CRASH` con `timeLostMs` | permite medir cuánto le costaron los errores a cada piloto |
| `MATCH_STARTED` con `track` y `laps` | contexto de la carrera |

Decisión clave: **las posiciones no se envían**. La posición de un piloto tras
la vuelta *k* se reconstruye ordenando a todos por tiempo acumulado hasta esa
vuelta. Eso permite detectar la remontada (el ganador estuvo último en alguna
vuelta) y calcular posiciones ganadas, sin que el juego tenga que mandar un
evento por cada adelantamiento. Es exactamente el ejemplo que da el enunciado:
no basta la posición final; con vueltas, tiempos y penalizaciones se calcula
consistencia, mejor vuelta y evolución.

### 1.5 `chase`

| Evento | Por qué está |
|---|---|
| `ROUTE_COMPUTED` con `optimalSteps` y `routeTurns` | la referencia de lo óptimo; también indica si el tablero tenía solución (`found`) |
| `PLAYER_MOVED` con `direction` y `turn` | el recorrido real del jugador |
| `OBSTACLE_HIT` con `livesLeft` | errores y vidas restantes |
| `TARGET_REACHED` con `steps` y `turns` | resultado del recorrido |

Juntar `ROUTE_COMPUTED` con `TARGET_REACHED` da la **eficiencia** (pasos
óptimos / pasos usados) y los giros de más. Los patrones `PERFECT_RUN`,
`CLOSE_CALL` (ganó tras quedar con una vida) y `NO_ROUTE` necesitan varios
eventos relacionados.

### 1.6 Agregar un juego nuevo

El enunciado pide que un juego nuevo no obligue a modificar todo el sistema:

- **Go no cambia.** El ingest valida solo los campos comunes y guarda `data`
  como JSON sin interpretarlo (`json.RawMessage`), así que acepta cualquier
  juego que respete el formato común.
- **Scala funciona sin cambios** gracias a `GenericAnalyzer` (conteos por
  jugador y por tipo de evento). Para estadísticas propias se escribe un objeto
  que implementa el trait `GameAnalyzer` y se agrega a la lista de `Analyzers`:
  una línea fuera del analizador nuevo.
- El panel ya muestra las partidas y patrones de cualquier juego en sus
  listas; para darle una pestaña propia con columnas se agrega su entrada al
  objeto `GAMES` de `index.html`.

---

## 2. Arquitectura

### 2.1 Componentes

| Programa | Lenguaje | Puerto | Papel |
|---|---|---|---|
| `ingest` | Go | 8080 | recibe, valida, agrupa por partida y entrega a Scala |
| `analyzer` | Scala | 8081 | analiza partidas, guarda y expone resultados |
| `simulator` | Go | – | fuente externa simulada: genera y "juega" partidas |
| `dashboard` | Go + HTML/JS | 8090 | panel web: muestra el flujo y los resultados, lanza el simulador |

Ingest y analyzer son **servicios independientes**: procesos (y contenedores)
separados que solo se conocen por HTTP. Ninguno importa código del otro; el
único acuerdo entre ellos es `CONTRACT.md`.

### 2.2 Por qué HTTP/REST

Alternativas consideradas: intercambio de archivos y mensajería (una cola).

- **Archivos**: obligan a compartir un volumen entre contenedores y a decidir
  cuándo un archivo está completo y quién lo borra. Más piezas para el mismo resultado.
- **Mensajería**: agrega un servicio más (un broker) que configurar, monitorear
  y documentar, para un volumen que HTTP maneja sin problema.
- **HTTP/REST**: Go lo trae en su librería estándar; en Scala basta una
  librería pequeña (Cask). Se prueba con `curl`, funciona igual en Docker
  Compose (cada servicio se encuentra por su nombre) y una fuente externa puede
  producir eventos sin saber nada de la implementación.

La desventaja de HTTP frente a una cola es que si Scala está caído, Go tiene que
reintentar por su cuenta. Eso se resolvió con reintentos (sección 3.5) y con el
orden de arranque de Compose (el ingest espera a que Scala esté sano).

### 2.3 Por qué Go entrega partidas completas

Go podría reenviar cada evento a Scala apenas llega. No se hizo así porque casi
todo el análisis interesante necesita **la partida entera**: el ganador, la
remontada, la posición vuelta a vuelta. Si Scala recibiera eventos sueltos,
tendría que guardar estado parcial y decidir cuándo una partida terminó: es
decir, repetir el trabajo de Go con estructuras mutables, que es justo lo que
no le corresponde.

Con partidas completas, analizar es una **función pura**: lista de eventos →
resultado. Además, reenviar la misma partida reemplaza su análisis anterior, así
que un reintento nunca duplica resultados.

---

## 3. Go: ingestión imperativa y concurrente

### 3.1 Recorrido de un evento

```
POST /events ──► validación ──► Submit ──► canal del trabajador N ──► trabajador N
 (una goroutine     (sin estado)              (hash del matchId)        (agrupa la partida)
  por petición)                                                             │ partida lista
                                                                            ▼
                                       Scala ◄── POST /analyze ◄── mensajeros ◄── canal ready
```

Código: `internal/api` (HTTP), `internal/event` (validación),
`internal/pipeline` (trabajadores y mensajeros), `internal/analyzer` (cliente
hacia Scala).

### 3.2 Validación en dos etapas

1. **Sin estado, al recibir** (`event.Validate`): campos obligatorios, formato
   de `timestamp`, `gameId`, `type` y `data`. Cada evento se valida por
   separado y se informan todos sus problemas. Es estricta a propósito: Scala
   rechaza la petición completa si un solo evento viene mal, así que un
   `timestamp` roto que se colara haría perder una partida entera.
2. **Con estado, al agrupar** (`pipeline/worker.go`): duplicados, eventos
   tardíos (la partida ya se envió) y eventos de otro juego. Estas reglas
   necesitan recordar lo que ya llegó, por eso viven en el dueño de la partida.

Todo lo descartado queda en `GET /rejected` con el motivo.

### 3.3 Un dueño por partida: trabajadores por hash

El pipeline arranca `WORKERS` goroutines trabajadoras (4 por defecto), cada una
con su propio canal. Cada evento se manda al trabajador `hash(matchId) % N`.

Consecuencia: **todos los eventos de una partida los procesa siempre el mismo
trabajador**, y ese trabajador es el único que toca el estado de sus partidas
(el mapa de partidas abiertas, los `eventId` vistos, los jugadores). No hay
datos compartidos entre trabajadores, así que **no hacen falta candados**: se
comunican solo por canales. Es la idea de Go de "compartir memoria
comunicándose" en vez de "comunicarse compartiendo memoria".

Partidas distintas sí se procesan en paralelo: varias partidas y varios juegos
avanzan al mismo tiempo en trabajadores distintos.

Cada trabajador es un ciclo con `select` que atiende tres cosas:
eventos nuevos, el reloj (cada segundo revisa partidas inactivas) y avisos de
partidas cuyo envío falló.

### 3.4 Cuándo una partida está lista

- Al llegar su `MATCH_FINISHED`.
- Si pasa `IDLE_TIMEOUT` (30 s) sin eventos: cubre partidas abandonadas o
  fuentes que se cortan. El simulador manda una carrera sin `MATCH_FINISHED`
  para demostrarlo.
- Al apagar el servicio: las partidas abiertas se envían antes de terminar.

Una partida ya enviada se recuerda como cerrada (con sus `eventId`) para poder
distinguir un **duplicado** (mismo `eventId` reenviado) de un **evento tardío**
(evento nuevo de una partida ya analizada).

### 3.5 Mensajeros y reintentos

`SENDERS` goroutines (2 por defecto) toman partidas listas del canal `ready` y
las envían a Scala. Están separadas de los trabajadores para que una espera de
red **no frene la agrupación** de otras partidas.

Si Scala no responde o da 5xx, el cliente reintenta con espera exponencial
(1 s, 2 s, 4 s, 8 s, 16 s). Un 4xx no se reintenta porque daría el mismo error.
Si al final falla, el mensajero avisa al trabajador dueño para que olvide la
partida como cerrada: si la fuente reenvía los eventos, se aceptan y se
reintenta. El aviso usa un `select` con `default` para que el mensajero nunca
se quede bloqueado.

### 3.6 Apagado ordenado

Con `SIGTERM` (lo que manda `docker compose down`) o `Ctrl+C`: el servidor HTTP
deja de aceptar peticiones, se cierran los canales de los trabajadores (que
envían sus partidas abiertas), luego el canal `ready`, y se espera con
`WaitGroup` a que los mensajeros terminen. No se pierden partidas a medias.

### 3.7 Dónde sí hay estado compartido

- **Contadores** de `/status`: `atomic.Int64`, porque los suman muchas
  goroutines y una suma atómica es más simple y barata que un candado.
- **Registro de rechazados**: `sync.Mutex`, porque cada petición HTTP corre en
  su propia goroutine y varias pueden escribir a la vez. Se devuelve una copia
  para que quien lee no choque con escrituras nuevas.

Las pruebas se corren con `go test -race`, que detecta accesos concurrentes sin
protección.

### 3.8 Simulador

Cada partida se juega en su **propia goroutine**, con pausas al azar entre
eventos, así que los eventos de varias partidas y juegos llegan intercalados
como en un sistema real. Las partidas se generan completas antes de enviarlas,
con una semilla: **la misma semilla produce las mismas partidas**, lo que
permite repetir una demostración o una prueba de CI.

Además de partidas al azar, incluye partidas guionizadas que garantizan cada
regla (remontada en combat y en racing, carrera perfecta y tablero sin ruta en
chase, carrera abandonada) y casos especiales para Go: eventos inválidos,
duplicados, un evento tardío y uno de otro juego dentro de una partida.

### 3.9 Panel

Programa aparte para no mezclar presentación con ingestión. Sirve una página
(HTML, CSS y JavaScript sin librerías, incluida en el binario con `go:embed`),
reenvía sus consultas a Go y a Scala (así el navegador habla con un solo origen
y no hacen falta permisos CORS en los servicios) y lanza el simulador como un
proceso aparte con `os/exec`, leyendo su salida línea por línea.

---

## 4. Scala: análisis funcional

### 4.1 Datos inmutables

Los eventos se decodifican con circe a `case class` inmutables
(`model/Model.scala`). Cada analizador es una función
`(matchId, List[GameEvent]) => MatchAnalysis`: no modifica nada, solo
transforma una colección en otra. Por eso se prueba con una lista de eventos
armada a mano y una comparación del resultado, sin servidor ni simulaciones.

### 4.2 Operaciones usadas y para qué

| Operación | Uso |
|---|---|
| `groupBy` | separar los eventos por `(gameId, matchId)`; vueltas por piloto |
| `groupMapReduce` | conteos y sumas por clave en un paso: eventos por tipo, daño por jugador, tiempo perdido por piloto |
| `collect` | filtrar y transformar a la vez: de todos los eventos, solo las vueltas, ya convertidas en `Lap` |
| `flatMap` sobre `Option` | leer campos opcionales de `data` sin `if`/`null` |
| `foldLeft` | `KILL_STREAK`: recorrer las eliminaciones llevando la racha de cada jugador como acumulador |
| `scanLeft` | `COMEBACK` de combat (marcador después de cada eliminación) y tiempo acumulado por vuelta en racing (de ahí sale la posición vuelta a vuelta) |
| `zipWithIndex` + `collectFirst` | primera vuelta en la que el ganador iba último |
| `minByOption`, `maxOption` | vuelta más rápida, déficit máximo; sin excepciones con listas vacías |

Ejemplo, la posición tras cada vuelta en racing (`RacingAnalyzer.standingsByLap`):

```scala
val cumulative = lapsByPlayer.map { (p, ls) =>
  p -> ls.sortBy(_.lap).scanLeft((0, 0.0))((acc, l) => (l.lap, acc._2 + l.timeMs)).tail
}
(1 to maxLap).toList.map { k =>
  cumulative.toList
    .flatMap((p, cs) => cs.find(_._1 == k).map(c => p -> c._2))  // tiempo de cada piloto en la vuelta k
    .sortBy(_._2)                                                 // menor tiempo = adelante
    .map(_._1)
}
```

### 4.3 `counters` y `derived`: composición

Cada analizador produce **contadores aditivos** (se pueden sumar entre
partidas) y una función `derive` que calcula ratios a partir de ellos. La misma
`derive` se usa para una partida y para el agregado de un jugador en todas sus
partidas (`ResultStore.playerSummaries`): primero se suman contadores con
`foldLeft`, después se aplica `derive`. Por eso, por ejemplo, la consistencia de
racing guarda la suma de cuadrados de las vueltas: así la desviación estándar
se puede recalcular después de sumar varias carreras, en vez de promediar
desviaciones (que daría un número incorrecto).

### 4.4 Estado del servicio

Los resultados se guardan en un `Map` inmutable dentro de un
`AtomicReference`: guardar es reemplazar el mapa entero por uno nuevo
(`updateAndGet`). Las lecturas concurrentes del servidor siempre ven un mapa
completo y consistente, sin candados.

---

## 5. Imperativo vs funcional

### 5.1 Reparto de responsabilidades

| Responsabilidad | Dónde | Por qué ese paradigma |
|---|---|---|
| Recibir peticiones, validar, rechazar | Go | trabajo de entrada/salida y decisiones evento por evento, con efectos (responder, registrar) |
| Agrupar por partida, decidir cuándo está lista | Go | es **estado que cambia** con cada evento y con el tiempo (timeouts): un mapa mutable con un solo dueño es lo más directo |
| Concurrencia y reintentos | Go | goroutines, canales y `select` modelan bien "esperar lo primero que pase" (evento, reloj, aviso de fallo) |
| Estadísticas y patrones | Scala | son **transformaciones** de una colección fija: entra una partida, sale un resultado, sin efectos |
| Agregado por jugador | Scala | agrupar y reducir; composición de `derive` sobre contadores sumados |

### 5.2 El mismo problema en los dos estilos

Contar eventos por tipo. En Go, de forma imperativa, se recorre y se modifica
un mapa:

```go
counts := map[string]int{}
for _, e := range events {
    counts[e.Type]++
}
```

En Scala, de forma funcional, se describe el resultado:

```scala
events.groupMapReduce(_.`type`)(_ => 1)(_ + _)
```

Detectar duplicados. En Go es natural porque los eventos llegan **de a uno y
en el tiempo**: el trabajador recuerda en un mapa los `eventId` vistos y decide
en el momento. En Scala habría que tener la colección completa
(`events.groupBy(_.eventId).filter(_._2.size > 1)`), que no sirve si los
eventos siguen llegando. La naturaleza del dato (flujo vs colección completa)
decidió el paradigma.

### 5.3 Ventajas y limitaciones observadas

**Imperativo (Go)**

- ➕ Modela con naturalidad el estado que evoluciona (partidas abiertas, timeouts).
- ➕ Control explícito de la concurrencia: se ve qué goroutine es dueña de qué.
- ➕ Rendimiento predecible y un binario pequeño sin dependencias.
- ➖ Más código para lo mismo: los cálculos que en Scala son una línea son un ciclo con variables.
- ➖ La corrección depende de disciplina (quién toca qué dato); por eso se usa
  `go test -race` y la regla de un dueño por partida.

**Funcional (Scala)**

- ➕ Las estadísticas se leen casi como su definición, y cada analizador es una
  función pura fácil de probar con una lista de eventos.
- ➕ Sin estado mutable no hay condiciones de carrera en el análisis.
- ➕ Componer (`derive` sobre contadores sumados) permite reutilizar la misma lógica por partida y por jugador.
- ➖ Algunas reglas secuenciales (rachas) obligan a cargar estado en el
  acumulador del `foldLeft`, menos directo que un ciclo con variables.
- ➖ Recalcular colecciones nuevas tiene un costo de memoria; para partidas de
  cientos o miles de eventos no importa, para millones habría que usar
  estructuras perezosas o streaming.
- ➖ La primera compilación y la imagen son más pesadas (JVM + sbt).

**En el sistema completo**, separar los paradigmas por servicio funcionó bien
porque el límite entre ellos es claro: Go entrega una partida completa e
inmutable, y desde ese momento todo es transformación. El costo es la
comunicación (serializar a JSON, HTTP, reintentos) y mantener el contrato al
día entre dos lenguajes.

---

## 6. Limitaciones conocidas

- **Persistencia en memoria**: al reiniciar Scala se pierden los resultados.
  Cambiarlo afectaría solo a `ResultStore`.
- **Un solo ingest**: el reparto por hash es dentro de un proceso. Para varias
  instancias habría que repartir por `matchId` antes de llegar al ingest (o
  usar una cola).
- **Partidas cerradas en memoria**: Go recuerda los `eventId` de las partidas
  ya enviadas para detectar duplicados y tardíos; en un servicio que corra
  meses habría que olvidarlas después de un tiempo.
- **El timeout usa la hora de llegada**, no el `timestamp` del evento: una
  fuente que manda eventos viejos de golpe no se ve afectada, pero una que se
  pausa más de `IDLE_TIMEOUT` a mitad de partida hace que la partida se envíe
  incompleta y lo que llegue después se rechaza como tardío.
- **Sin autenticación**: cualquiera que alcance el puerto 8080 puede enviar eventos.
