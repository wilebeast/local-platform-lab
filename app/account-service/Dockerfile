FROM golang:1.26-alpine AS builder
WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct
COPY api/account ./api/account
COPY pkg/serviceregistry ./pkg/serviceregistry
COPY app/account-service/go.mod app/account-service/go.sum ./app/account-service/
WORKDIR /src/app/account-service
RUN go mod download
COPY app/account-service .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/account-service ./cmd/account-service

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=builder /out/account-service /app/account-service
COPY app/account-service/etc/account-service.yaml /app/etc/account-service.yaml
USER app
EXPOSE 9002
ENTRYPOINT ["/app/account-service", "-f", "/app/etc/account-service.yaml"]
