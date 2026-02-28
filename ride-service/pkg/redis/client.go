package redis

import (
	"context"
	"fmt"
	"log"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Client wraps the Redis connection.
type Client struct {
	rdb *goredis.Client
}

// NewClient connects to Redis with retry.
func NewClient(addr string) (*Client, error) {
	rdb := goredis.NewClient(&goredis.Options{Addr: addr})
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := rdb.Ping(ctx).Err(); err == nil {
			cancel()
			log.Println("Connected to Redis")
			return &Client{rdb: rdb}, nil
		}
		cancel()
		log.Printf("Waiting for Redis... (%d/20)", i+1)
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("redis: failed to connect after 20 attempts")
}

// SetDriverLocation stores a driver's position in a Redis GEO set.
func (c *Client) SetDriverLocation(ctx context.Context, driverID string, lat, lng float64) error {
	return c.rdb.GeoAdd(ctx, "driver:locations", &goredis.GeoLocation{
		Name:      driverID,
		Longitude: lng,
		Latitude:  lat,
	}).Err()
}

// GetNearbyDrivers returns driver IDs within radiusKm of (lat,lng).
func (c *Client) GetNearbyDrivers(ctx context.Context, lat, lng, radiusKm float64, count int) ([]string, error) {
	res, err := c.rdb.GeoSearch(ctx, "driver:locations", &goredis.GeoSearchQuery{
		Longitude:  lng,
		Latitude:   lat,
		Radius:     radiusKm,
		RadiusUnit: "km",
		Count:      count,
		Sort:       "ASC",
	}).Result()
	if err != nil {
		return nil, err
	}
	return res, nil
}

// RemoveDriverLocation removes a driver from the GEO set (e.g. when assigned).
func (c *Client) RemoveDriverLocation(ctx context.Context, driverID string) error {
	return c.rdb.ZRem(ctx, "driver:locations", driverID).Err()
}

// CacheTrip stores trip data in a hash with TTL.
func (c *Client) CacheTrip(ctx context.Context, tripID string, data map[string]string) error {
	key := "trip:" + tripID
	pipe := c.rdb.Pipeline()
	pipe.HSet(ctx, key, data)
	pipe.Expire(ctx, key, 24*time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}

// GetCachedTrip retrieves a cached trip hash.
func (c *Client) GetCachedTrip(ctx context.Context, tripID string) (map[string]string, error) {
	return c.rdb.HGetAll(ctx, "trip:"+tripID).Result()
}

// Close tears down the Redis connection.
func (c *Client) Close() error { return c.rdb.Close() }
