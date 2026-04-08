package redis

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Client wraps the Redis connection.
type Client struct {
	rdb *goredis.Client
}

// NewClient connects to Redis with retry.
// addr may be a plain "host:port" (local) or a full rediss:// / redis:// URL (Upstash / managed).
func NewClient(addr string) (*Client, error) {
	var rdb *goredis.Client
	if strings.HasPrefix(addr, "redis://") || strings.HasPrefix(addr, "rediss://") {
		opts, err := goredis.ParseURL(addr)
		if err != nil {
			return nil, fmt.Errorf("redis: invalid URL: %w", err)
		}
		opts.PoolSize = 10
		opts.ReadTimeout = 3 * time.Second
		opts.WriteTimeout = 3 * time.Second
		opts.MaxRetries = 2
		rdb = goredis.NewClient(opts)
	} else {
		rdb = goredis.NewClient(&goredis.Options{
			Addr:         addr,
			PoolSize:     10,
			ReadTimeout:  3 * time.Second,
			WriteTimeout: 3 * time.Second,
			MaxRetries:   2,
		})
	}
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

// SaveDriverLocation stores a driver's last-known lat/lng as a plain key so it
// can be restored to the GEO pool after trip completion or cancellation.
func (c *Client) SaveDriverLocation(ctx context.Context, driverID string, lat, lng float64) error {
	key := fmt.Sprintf("driver:loc:%s", driverID)
	return c.rdb.Set(ctx, key, fmt.Sprintf("%f,%f", lat, lng), 24*time.Hour).Err()
}

// GetDriverLocation retrieves the last-saved lat/lng for a driver.
func (c *Client) GetDriverLocation(ctx context.Context, driverID string) (float64, float64, error) {
	key := fmt.Sprintf("driver:loc:%s", driverID)
	val, err := c.rdb.Get(ctx, key).Result()
	if err != nil {
		return 0, 0, err
	}
	parts := strings.SplitN(val, ",", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid location format for driver %s", driverID)
	}
	lat, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, 0, err
	}
	lng, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, 0, err
	}
	return lat, lng, nil
}

// LockDriver attempts to acquire an exclusive lock for a driver using SETNX.
// Returns true if the lock was acquired, false if the driver is already locked.
func (c *Client) LockDriver(ctx context.Context, driverID string, ttl time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, "driver:lock:"+driverID, "1", ttl).Result()
}

// UnlockDriver releases the exclusive lock for a driver.
func (c *Client) UnlockDriver(ctx context.Context, driverID string) error {
	return c.rdb.Del(ctx, "driver:lock:"+driverID).Err()
}

// GetDriverGeoPos retrieves a driver's current coordinates from the GEO set.
func (c *Client) GetDriverGeoPos(ctx context.Context, driverID string) (float64, float64, error) {
	positions, err := c.rdb.GeoPos(ctx, "driver:locations", driverID).Result()
	if err != nil || len(positions) == 0 || positions[0] == nil {
		return 0, 0, fmt.Errorf("driver %s not in GEO set", driverID)
	}
	return positions[0].Latitude, positions[0].Longitude, nil
}

// GetSurge returns the surge multiplier from the key "surge:multiplier".
// Falls back to 1.0 if the key is absent or unparseable.
// Set via: redis-cli SET surge:multiplier 1.5
func (c *Client) GetSurge(ctx context.Context) float64 {
	val, err := c.rdb.Get(ctx, "surge:multiplier").Result()
	if err != nil {
		return 1.0
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil || f <= 0 {
		return 1.0
	}
	const maxSurge = 5.0
	if f > maxSurge {
		return maxSurge
	}
	return f
}

// Ping checks the Redis connection.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// RDB returns the underlying go-redis client for packages that need direct access (e.g. otp).
func (c *Client) RDB() goredis.UniversalClient { return c.rdb }

// Close tears down the Redis connection.
func (c *Client) Close() error { return c.rdb.Close() }
