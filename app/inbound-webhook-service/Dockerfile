FROM golang:1.26-alpine AS builder
WORKDIR /src/app/inbound-webhook-service
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY
COPY app/inbound-webhook-service/go.mod app/inbound-webhook-service/go.sum ./
RUN go mod download
COPY app/inbound-webhook-service .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/inbound-webhook ./cmd/inbound-webhook

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
USER app
COPY --from=builder /out/inbound-webhook /inbound-webhook
EXPOSE 8080
ENTRYPOINT ["/inbound-webhook"]
