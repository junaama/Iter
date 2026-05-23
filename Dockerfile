FROM golang:1.25-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/server ./cmd/server
RUN GOBIN=/out go install github.com/pressly/goose/v3/cmd/goose@v3.27.1

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=build /out/server /usr/local/bin/server
COPY --from=build /out/goose /usr/local/bin/goose
COPY migrations ./migrations

ENV PORT=8080

CMD ["sh", "-c", "goose -dir /app/migrations postgres \"$DATABASE_URL_SUPERUSER\" up && exec server"]
