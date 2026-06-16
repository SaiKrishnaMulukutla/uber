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
		opts.PoolSize = 100
		opts.MinIdleConns = 10
		opts.ConnMaxIdleTime = 30 * time.Second
		opts.DialTimeout = 10 * time.Second
		opts.ReadTimeout = 5 * time.Second
		opts.WriteTimeout = 5 * time.Second
		opts.MaxRetries = 5
		rdb = goredis.NewClient(opts)
	} else {
		rdb = goredis.NewClient(&goredis.Options{
			Addr:            addr,
			PoolSize:        100,
			MinIdleConns:    10,
			ConnMaxIdleTime: 30 * time.Second,
			DialTimeout:     10 * time.Second,
			ReadTimeout:     5 * time.Second,
			WriteTimeout:    5 * time.Second,
			MaxRetries:      5,
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

// SetDriverType caches a driver's vehicle category (go | x | xl) in Redis.
// Used by matching-service to filter candidates without a DB lookup.
func (c *Client) SetDriverType(ctx context.Context, driverID, category string) error {
	return c.rdb.Set(ctx, "driver:type:"+driverID, category, 0).Err()
}

// GetDriverType retrieves a driver's vehicle category. Returns "" on miss.
func (c *Client) GetDriverType(ctx context.Context, driverID string) string {
	v, _ := c.rdb.Get(ctx, "driver:type:"+driverID).Result()
	return v
}

// SetOffer stores the chosen driverID as the pending offer for tripID with a TTL.
func (c *Client) SetOffer(ctx context.Context, tripID, driverID string, ttl time.Duration) error {
	return c.rdb.Set(ctx, "offer:"+tripID, driverID, ttl).Err()
}

// GetOffer returns the driverID for a pending offer, or an error if none exists.
func (c *Client) GetOffer(ctx context.Context, tripID string) (string, error) {
	return c.rdb.Get(ctx, "offer:"+tripID).Result()
}

// DeleteOffer removes a pending offer and its associated event payload.
func (c *Client) DeleteOffer(ctx context.Context, tripID string) error {
	return c.rdb.Del(ctx, "offer:"+tripID, "offer:req:"+tripID).Err()
}

// SetOfferEvent stores the original ride.requested event JSON alongside the offer.
// Used by driver-service to re-queue the trip on rejection.
func (c *Client) SetOfferEvent(ctx context.Context, tripID string, data []byte, ttl time.Duration) error {
	return c.rdb.Set(ctx, "offer:req:"+tripID, data, ttl).Err()
}

// GetOfferEvent retrieves the original ride.requested event JSON for a pending offer.
func (c *Client) GetOfferEvent(ctx context.Context, tripID string) ([]byte, error) {
	return c.rdb.Get(ctx, "offer:req:"+tripID).Bytes()
}

// GetSurge returns the surge multiplier from the key "surge:multiplier".
// Falls back to 1.0 if the key is absent or unparseable.
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

// SetSurge persists a new surge multiplier to the key "surge:multiplier".
// The value is stored without a TTL so it survives restarts.
func (c *Client) SetSurge(ctx context.Context, multiplier float64) error {
	return c.rdb.Set(ctx, "surge:multiplier", strconv.FormatFloat(multiplier, 'f', 2, 64), 0).Err()
}

const tripOTPTTL = 2 * time.Hour

// SetTripOTP stores the ride confirmation OTP for a trip, keyed by tripID.
func (c *Client) SetTripOTP(ctx context.Context, tripID, otp string) error {
	return c.rdb.Set(ctx, "trip:otp:"+tripID, otp, tripOTPTTL).Err()
}

// GetTripOTP retrieves the ride confirmation OTP for a trip.
func (c *Client) GetTripOTP(ctx context.Context, tripID string) (string, error) {
	return c.rdb.Get(ctx, "trip:otp:"+tripID).Result()
}

// DeleteTripOTP removes the ride confirmation OTP after it has been consumed.
func (c *Client) DeleteTripOTP(ctx context.Context, tripID string) error {
	return c.rdb.Del(ctx, "trip:otp:"+tripID).Err()
}

const refreshTokenKeyPrefix = "rt:"

// StoreRefreshToken persists a refresh token JTI so it can be validated and revoked.
func (c *Client) StoreRefreshToken(ctx context.Context, jti string, ttl time.Duration) error {
	return c.rdb.Set(ctx, refreshTokenKeyPrefix+jti, "1", ttl).Err()
}

// IsRefreshTokenValid returns true if the JTI is still present in the store.
func (c *Client) IsRefreshTokenValid(ctx context.Context, jti string) bool {
	n, err := c.rdb.Exists(ctx, refreshTokenKeyPrefix+jti).Result()
	return err == nil && n == 1
}

// RevokeRefreshToken deletes the JTI, invalidating the token immediately.
func (c *Client) RevokeRefreshToken(ctx context.Context, jti string) {
	c.rdb.Del(ctx, refreshTokenKeyPrefix+jti)
}

// Ping checks the Redis connection.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// RDB returns the underlying go-redis client for packages that need direct access (e.g. otp).
func (c *Client) RDB() goredis.UniversalClient { return c.rdb }

// Close tears down the Redis connection.
func (c *Client) Close() error { return c.rdb.Close() }
