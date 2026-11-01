FROM golang:1.26-alpine AS builder
WORKDIR /src/app/risk-control-service
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY
COPY app/risk-control-service/go.mod app/risk-control-service/go.sum ./
RUN go mod download
COPY app/risk-control-service .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/risk-control ./cmd/risk-control && \
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/offline-risk ./cmd/offline-risk && \
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/risk-action-worker ./cmd/risk-action-worker && \
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/risk-nearline-worker ./cmd/risk-nearline-worker && \
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/risk-raw-sink ./cmd/risk-raw-sink && \
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/risk-warehouse-compactor ./cmd/risk-warehouse-compactor && \
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/risk-feature-snapshot ./cmd/risk-feature-snapshot && \
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/risk-feature-validator ./cmd/risk-feature-validator && \
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/risk-feature-evaluator ./cmd/risk-feature-evaluator && \
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/risk-warehouse-run ./cmd/risk-warehouse-run && \
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/risk-warehouse-backfill ./cmd/risk-warehouse-backfill && \
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/risk-lineage-auditor ./cmd/risk-lineage-auditor && \
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/risk-feature-reconciler ./cmd/risk-feature-reconciler

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
USER app
COPY --from=builder /out/risk-control /risk-control
COPY --from=builder /out/offline-risk /offline-risk
COPY --from=builder /out/risk-action-worker /risk-action-worker
COPY --from=builder /out/risk-nearline-worker /risk-nearline-worker
COPY --from=builder /out/risk-raw-sink /risk-raw-sink
COPY --from=builder /out/risk-warehouse-compactor /risk-warehouse-compactor
COPY --from=builder /out/risk-feature-snapshot /risk-feature-snapshot
COPY --from=builder /out/risk-feature-validator /risk-feature-validator
COPY --from=builder /out/risk-feature-evaluator /risk-feature-evaluator
COPY --from=builder /out/risk-warehouse-run /risk-warehouse-run
COPY --from=builder /out/risk-warehouse-backfill /risk-warehouse-backfill
COPY --from=builder /out/risk-lineage-auditor /risk-lineage-auditor
COPY --from=builder /out/risk-feature-reconciler /risk-feature-reconciler
EXPOSE 8080
ENTRYPOINT ["/risk-control"]
