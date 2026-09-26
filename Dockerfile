FROM golang:1.25.13-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party ./third_party
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bot ./cmd/bot \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/extract-backfill ./cmd/extract-backfill

FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata && addgroup -S app && adduser -S -G app app
COPY --from=build /out/bot /usr/local/bin/bot
COPY --from=build /out/extract-backfill /usr/local/bin/extract-backfill
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/bot"]
