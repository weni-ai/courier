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

// sessionEntry represents a cache entry with metadata
type sessionEntry struct {
	session   *session.Session
	lastUsed  time.Time
	createdAt time.Time
	region    string
	useCount  int64
}

// sessionCache maintains a cache of AWS sessions by region
type sessionCache struct {
	sync.RWMutex
	sessions    map[string]*sessionEntry
	stopCleanup chan struct{}
	stopped     bool // flag to track if cache was stopped
	log         *logrus.Entry
}

var (
	// global session cache instance
	globalSessionCache = newSessionCache()
)

// newSessionCache creates and initializes a new session cache
func newSessionCache() *sessionCache {
	cache := &sessionCache{
		sessions:    make(map[string]*sessionEntry),
		stopCleanup: make(chan struct{}),
		stopped:     false,
		log:         logrus.WithField("component", "aws-session-cache"),
	}

	// Start the cleanup routine
	go cache.startCleanup()

	return cache
}

// Stop stops the cache cleanup routine
func (c *sessionCache) Stop() {
	c.Lock()
	defer c.Unlock()

	if !c.stopped {
		c.log.Info("stopping session cache cleanup routine")
		close(c.stopCleanup)
		c.stopped = true
	}
}

// startCleanup starts the periodic cache cleanup routine
func (c *sessionCache) startCleanup() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.cleanup()
		case <-c.stopCleanup:
			return
		}
	}
}

// cleanup removes expired sessions from the cache
func (c *sessionCache) cleanup() {
	c.Lock()
	defer c.Unlock()

	now := time.Now()
	removedCount := 0

	for key, entry := range c.sessions {
		// Remove sessions that:
		// 1. Exceeded 24 hours lifetime
		// 2. Haven't been used in the last 6 hours
		if now.Sub(entry.createdAt) > 24*time.Hour ||
			now.Sub(entry.lastUsed) > 6*time.Hour {

			delete(c.sessions, key)
			removedCount++

			c.log.WithFields(logrus.Fields{
				"region":   entry.region,
				"age":      now.Sub(entry.createdAt).String(),
				"lastUsed": now.Sub(entry.lastUsed).String(),
				"useCount": entry.useCount,
			}).Debug("removed expired session from cache")
		}
	}

	if removedCount > 0 {
		c.log.WithField("removedCount", removedCount).Info("cleaned up expired sessions")
	}
}

// getSession returns an existing AWS session from cache or creates a new one
func (c *sessionCache) getSession(region string, accessKey string, secretKey string) (*session.Session, error) {
	cacheKey := fmt.Sprintf("%s:%s", region, accessKey) // For IAM role, accessKey will be empty

	// First, try to read from cache
	c.RLock()
	if entry, ok := c.sessions[cacheKey]; ok {
		// Update session metadata
		entry.lastUsed = time.Now()
		entry.useCount++

		c.RUnlock()

		c.log.WithFields(logrus.Fields{
			"region":   entry.region,
			"useCount": entry.useCount,
			"age":      time.Since(entry.createdAt).String(),
		}).Debug("cache hit: reusing existing session")

		return entry.session, nil
	}
	c.RUnlock()

	// If not found in cache, create a new session
	c.Lock()
	defer c.Unlock()

	// Check again after acquiring exclusive lock
	if entry, ok := c.sessions[cacheKey]; ok {
		return entry.session, nil
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
	c.sessions[cacheKey] = &sessionEntry{
		session:   sess,
		lastUsed:  now,
		createdAt: now,
		region:    region,
		useCount:  1,
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
	sess, err := globalSessionCache.getSession(region, "", "")
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
