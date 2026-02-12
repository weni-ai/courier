package courier

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/sirupsen/logrus"
)

// SessionEntry represents a cache entry with metadata
type SessionEntry struct {
	Session   *session.Session
	LastUsed  time.Time
	CreatedAt time.Time
	Region    string
	UseCount  int64
}

// IsS3URL checks if the URL belongs to the configured S3 bucket
func IsS3URL(url string, bucket string) bool {
	// Virtual-hosted-style format: https://bucket-name.s3.region.amazonaws.com/key
	// or https://bucket-name.s3.amazonaws.com/key
	if strings.Contains(url, bucket+".s3.") {
		return true
	}
	// Path-style format: https://s3.region.amazonaws.com/bucket-name/key
	if strings.Contains(url, "/"+bucket+"/") {
		return true
	}
	return false
}

// SessionCache maintains a cache of AWS sessions by region
type SessionCache struct {
	sync.RWMutex
	Sessions    map[string]*SessionEntry
	stopCleanup chan struct{}
	stopped     bool // flag to track if cache was stopped
	log         *logrus.Entry
}

var (
	// global session cache instance
	globalSessionCache = NewSessionCache()
)

// NewSessionCache creates and initializes a new session cache
func NewSessionCache() *SessionCache {
	cache := &SessionCache{
		Sessions:    make(map[string]*SessionEntry),
		stopCleanup: make(chan struct{}),
		stopped:     false,
		log:         logrus.WithField("component", "aws-session-cache"),
	}

	// Start the cleanup routine
	go cache.startCleanup()

	return cache
}

// Stop stops the cache cleanup routine
func (c *SessionCache) Stop() {
	c.Lock()
	defer c.Unlock()

	if !c.stopped {
		c.log.Info("stopping session cache cleanup routine")
		close(c.stopCleanup)
		c.stopped = true
	}
}

// startCleanup starts the periodic cache cleanup routine
func (c *SessionCache) startCleanup() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.Cleanup()
		case <-c.stopCleanup:
			return
		}
	}
}

// Cleanup removes expired sessions from the cache
func (c *SessionCache) Cleanup() {
	c.Lock()
	defer c.Unlock()

	now := time.Now()
	removedCount := 0

	for key, entry := range c.Sessions {
		// Remove sessions that:
		// 1. Exceeded 24 hours lifetime
		// 2. Haven't been used in the last 6 hours
		if now.Sub(entry.CreatedAt) > 24*time.Hour ||
			now.Sub(entry.LastUsed) > 6*time.Hour {

			delete(c.Sessions, key)
			removedCount++

			c.log.WithFields(logrus.Fields{
				"region":   entry.Region,
				"age":      now.Sub(entry.CreatedAt).String(),
				"lastUsed": now.Sub(entry.LastUsed).String(),
				"useCount": entry.UseCount,
			}).Debug("removed expired session from cache")
		}
	}

	if removedCount > 0 {
		c.log.WithField("count", removedCount).Info("cleaned up expired sessions")
	}
}

// GetSession returns an existing AWS session from cache or creates a new one
func (c *SessionCache) GetSession(region string, accessKey string, secretKey string) (*session.Session, error) {
	cacheKey := fmt.Sprintf("%s:%s:%s", accessKey, region, secretKey)

	// First, try to read from cache
	c.RLock()
	entry, ok := c.Sessions[cacheKey]
	c.RUnlock()

	if ok {
		// Update session metadata with write lock to avoid race condition
		c.Lock()
		entry.LastUsed = time.Now()
		entry.UseCount++
		useCount := entry.UseCount
		createdAt := entry.CreatedAt
		c.Unlock()

		c.log.WithFields(logrus.Fields{
			"region":   entry.Region,
			"useCount": useCount,
			"age":      time.Since(createdAt).String(),
		}).Debug("cache hit: reusing existing session")

		return entry.Session, nil
	}

	// If not found in cache, create a new session
	c.Lock()
	defer c.Unlock()

	// Check again after acquiring exclusive lock
	if entry, ok := c.Sessions[cacheKey]; ok {
		return entry.Session, nil
	}

	// Create AWS config
	awsConfig := &aws.Config{
		Region: aws.String(region),
	}

	// If static credentials are provided, use them
	if accessKey != "" && secretKey != "" {
		awsConfig.Credentials = credentials.NewStaticCredentials(accessKey, secretKey, "")
	}

	// Create a new session
	sess, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS session: %w", err)
	}

	// Store in cache with metadata
	now := time.Now()
	c.Sessions[cacheKey] = &SessionEntry{
		Session:   sess,
		LastUsed:  now,
		CreatedAt: now,
		Region:    region,
		UseCount:  1,
	}

	c.log.WithField("region", region).Debug("cache miss: created new session")

	return sess, nil
}

// PresignedURL generates a pre-signed URL for an S3 object with configurable expiration
func PresignedURL(link string, accessKey string, secretKey string, region string, expirationHours int) (string, error) {
	// Validate input parameters
	if link == "" {
		return "", fmt.Errorf("empty link provided")
	}
	if region == "" {
		return "", fmt.Errorf("AWS region not provided")
	}
	if expirationHours <= 0 {
		expirationHours = 168 // default to 7 days if not specified
	}

	// Parse the URL
	parsedURL, err := url.Parse(link)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return "", fmt.Errorf("invalid URL format: URL must be absolute with scheme and host")
	}

	// Extract bucket name from host
	bucketName := strings.Split(parsedURL.Host, ".")[0]
	if bucketName == "" {
		return "", fmt.Errorf("could not extract bucket name from URL")
	}

	// Extract object key from path
	objectKey := parsedURL.Path
	if !strings.HasPrefix(objectKey, "/") {
		objectKey = "/" + objectKey
	}

	// Get cached session or create new one - passing empty credentials to force IAM role usage
	sess, err := globalSessionCache.GetSession(region, accessKey, secretKey)
	if err != nil {
		return "", err
	}

	svc := s3.New(sess)

	// Create the request for pre-signing
	input := &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	}
	req, _ := svc.GetObjectRequest(input)

	// Generate pre-signed URL with the specified expiration
	urlStr, err := req.Presign(time.Duration(expirationHours) * time.Hour)
	if err != nil {
		return "", fmt.Errorf("failed to generate pre-signed URL: %w", err)
	}

	return urlStr, nil
}

// SplitAttachment takes an attachment string and returns the media type and URL for the attachment
func SplitAttachment(attachment string) (string, string) {
	parts := strings.SplitN(attachment, ":", 2)
	if len(parts) < 2 {
		return "", parts[0]
	}
	return parts[0], parts[1]
}
