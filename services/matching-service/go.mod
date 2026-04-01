module uber/matching-service

go 1.21

require uber/shared v0.0.0

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/klauspost/compress v1.15.9 // indirect
	github.com/pierrec/lz4/v4 v4.1.15 // indirect
	github.com/redis/go-redis/v9 v9.4.0 // indirect
	github.com/segmentio/kafka-go v0.4.47 // indirect
)

replace uber/shared => ../../shared
