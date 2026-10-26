FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor -trimpath -o /out/acquiring-service ./cmd/acquiring

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/acquiring-service /acquiring-service
EXPOSE 8080
ENTRYPOINT ["/acquiring-service"]
