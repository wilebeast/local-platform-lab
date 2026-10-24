FROM golang:1.26-alpine AS builder
WORKDIR /src/app/enterprise-card-service
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY
COPY app/enterprise-card-service/go.mod app/enterprise-card-service/go.sum ./
RUN go mod download
COPY app/enterprise-card-service .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/enterprise-card ./cmd/enterprise-card

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
USER app
COPY --from=builder /out/enterprise-card /enterprise-card
EXPOSE 8080
ENTRYPOINT ["/enterprise-card"]
