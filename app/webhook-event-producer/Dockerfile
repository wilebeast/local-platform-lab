FROM golang:1.26-alpine AS builder
WORKDIR /src/app/webhook-event-producer
ENV GOPROXY=https://goproxy.cn,direct
COPY app/webhook-event-producer/go.mod app/webhook-event-producer/go.sum ./
RUN go mod download
COPY app/webhook-event-producer .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/producer ./cmd/producer

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
USER app
COPY --from=builder /out/producer /producer
EXPOSE 8080
ENTRYPOINT ["/producer"]
