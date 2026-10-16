# Linux containers are required (Docker Desktop + WSL 2 on Windows).
# SQLite uses CGO, so build and runtime both include compatible SQLite libraries.
FROM golang:1.27-bookworm AS build
RUN apt-get update \
    && apt-get install -y --no-install-recommends libsqlite3-dev \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod *.go openapi.yaml openapi.json ./
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /grocery-api .

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends libsqlite3-0 ca-certificates tzdata curl \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 grocery \
    && useradd --uid 10001 --gid grocery --no-create-home --shell /usr/sbin/nologin grocery \
    && mkdir -p /app/data /app/imports /app/demo \
    && chown -R grocery:grocery /app
WORKDIR /app
COPY --from=build /grocery-api /usr/local/bin/grocery-api
# Bundle default source definitions so Windows users do not need host-file mounts.
# After editing sources.json, rebuild the image: docker compose up --build -d
COPY sources.json sources.demo.json ./
COPY demo/ ./demo/
USER grocery
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/grocery-api"]
CMD ["-config", "/app/sources.json", "-db", "/app/data/groceries.sqlite"]
