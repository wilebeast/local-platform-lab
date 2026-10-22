FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY app/merchant-webhook-receiver ./app/merchant-webhook-receiver
WORKDIR /src/app/merchant-webhook-receiver
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/receiver ./cmd/receiver

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
USER app
COPY --from=builder /out/receiver /receiver
EXPOSE 8080
ENTRYPOINT ["/receiver"]
