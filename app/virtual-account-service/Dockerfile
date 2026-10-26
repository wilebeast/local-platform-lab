FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/service ./cmd/virtual-account
FROM alpine:3.22
COPY --from=build /out/service /service
USER 65532:65532
ENTRYPOINT ["/service"]
