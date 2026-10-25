FROM golang:1.26-alpine AS builder
WORKDIR /src/app/risk-control-service
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY
COPY app/risk-control-service/go.mod app/risk-control-service/go.sum ./
RUN go mod download
COPY app/risk-control-service .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/risk-control ./cmd/risk-control

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
USER app
COPY --from=builder /out/risk-control /risk-control
EXPOSE 8080
ENTRYPOINT ["/risk-control"]
