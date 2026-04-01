module uber/otp-service

go 1.21

require (
	github.com/go-chi/chi/v5 v5.0.12
	github.com/redis/go-redis/v9 v9.4.0
	uber/shared v0.0.0
)

replace uber/shared => ../../shared

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
)
