FROM golang:1.26-alpine AS builder
WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct
COPY api/user ./api/user
COPY pkg/serviceregistry ./pkg/serviceregistry
COPY app/api-bff/go.mod app/api-bff/go.sum ./app/api-bff/
WORKDIR /src/app/api-bff
RUN go mod download
COPY app/api-bff .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api-bff ./cmd/api-bff

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=builder /out/api-bff /app/api-bff
COPY app/api-bff/etc/api-bff.yaml /app/etc/api-bff.yaml
USER app
EXPOSE 8888
ENTRYPOINT ["/app/api-bff", "-f", "/app/etc/api-bff.yaml"]
