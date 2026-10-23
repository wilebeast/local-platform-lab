module github.com/wilebeast/local-platform-lab/app/notification-service

go 1.26

require (
	github.com/jackc/pgx/v5 v5.8.0
	github.com/redis/go-redis/v9 v9.21.0
	github.com/wilebeast/local-platform-lab/api/notification v0.0.0
	google.golang.org/grpc v1.80.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
	golang.org/x/text v0.33.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260120221211-b8f7ae30c516 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/wilebeast/local-platform-lab/api/notification => ../../api/notification
