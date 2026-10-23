FROM golang:1.26-alpine AS builder
WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct
COPY api/notification ./api/notification
COPY app/notification-service/go.mod app/notification-service/go.sum ./app/notification-service/
WORKDIR /src/app/notification-service
RUN go mod download
COPY app/notification-service .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/notification-service ./cmd/notification-service

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=builder /out/notification-service /app/notification-service
USER app
EXPOSE 9003 8080
ENTRYPOINT ["/app/notification-service"]
