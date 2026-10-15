# Imagen del módulo Go: trae los tres programas (ingest, simulador y panel).
# Una sola imagen para los tres, porque comparten código y se compilan juntos.
# Docker Compose decide cuál corre cada contenedor con "command"; por defecto, el ingest.
#
#   docker build -t gamestats-go ingest-go
#   docker run -p 8080:8080 -e ANALYZER_URL=http://host.docker.internal:8081 gamestats-go
#
# Las pruebas no corren aquí: necesitan ../samples, que queda fuera de esta carpeta.
# Las corre el CI con go test -race antes de construir la imagen.

# --- Etapa 1: compilar ---
FROM golang:1.24-alpine AS build
WORKDIR /src

# Primero solo go.mod: si las dependencias no cambian, Docker reutiliza esta capa
# y no las vuelve a descargar en cada build.
COPY go.mod ./
RUN go mod download

COPY . .
# CGO_ENABLED=0: binarios estáticos, que no dependen de librerías del sistema.
# -trimpath y -ldflags="-s -w": quitan rutas locales e información de depuración (binarios más chicos).
RUN for cmd in ingest simulator dashboard; do \
      CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/$cmd ./cmd/$cmd || exit 1; \
    done

# --- Etapa 2: ejecutar ---
# Alpine pesa unos 8 MB y trae wget, que usan los healthchecks de Compose.
# El compilador de Go (unos 300 MB) se queda en la etapa 1 y no viaja en la imagen final.
FROM alpine:3.20
RUN adduser -D -u 10001 gamestats
WORKDIR /app
# Los tres binarios en la misma carpeta: el panel busca "simulator" junto a sí mismo.
COPY --from=build /out/ /app/
# Sin permisos de administrador: si alguien encontrara una falla, no controla el contenedor.
USER gamestats

EXPOSE 8080 8090
CMD ["/app/ingest"]
