FROM golang:1.26-alpine AS builder
WORKDIR /src/app/webhook-sender
ENV GOPROXY=https://goproxy.cn,direct
COPY app/webhook-sender/go.mod app/webhook-sender/go.sum ./
RUN go mod download
COPY app/webhook-sender .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/sender ./cmd/sender

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
USER app
COPY --from=builder /out/sender /sender
ENTRYPOINT ["/sender"]
