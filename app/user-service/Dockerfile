FROM golang:1.26-alpine AS builder
WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct
COPY api/user ./api/user
COPY pkg/serviceregistry ./pkg/serviceregistry
COPY app/user-service/go.mod app/user-service/go.sum ./app/user-service/
WORKDIR /src/app/user-service
RUN go mod download
COPY app/user-service .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/user-service ./cmd/user-service

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=builder /out/user-service /app/user-service
COPY app/user-service/etc/user-service.yaml /app/etc/user-service.yaml
USER app
EXPOSE 9001
ENTRYPOINT ["/app/user-service", "-f", "/app/etc/user-service.yaml"]
