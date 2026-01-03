package courier_test

import (
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/stretchr/testify/assert"
)

func TestSessionCache(t *testing.T) {
	// Create a new cache instance for testing
	cache := courier.NewSessionCache()
	defer cache.Stop()

	t.Run("GetSession", func(t *testing.T) {
		// Test getting a new session
		sess1, err := cache.GetSession("us-east-1", "test-key", "test-secret")
		assert.NoError(t, err)
		assert.NotNil(t, sess1)

		// Test cache hit - should return the same session
		sess2, err := cache.GetSession("us-east-1", "test-key", "test-secret")
		assert.NoError(t, err)
		assert.NotNil(t, sess2)

		// Test different region - should create new session
		sess3, err := cache.GetSession("us-west-1", "test-key", "test-secret")
		assert.NoError(t, err)
		assert.NotNil(t, sess3)
	})

	t.Run("SessionMetadata", func(t *testing.T) {
		cache := courier.NewSessionCache()
		defer cache.Stop()

		// Get a session and check its metadata
		sess, err := cache.GetSession("us-east-1", "test-key", "test-secret")
		assert.NoError(t, err)
		assert.NotNil(t, sess)

		// Check session entry metadata
		cache.RLock()
		key := "test-key:us-east-1:test-secret"
		entry, exists := cache.Sessions[key]
		cache.RUnlock()

		assert.True(t, exists, "session should exist in cache")
		if assert.NotNil(t, entry, "session entry should not be nil") {
			assert.Equal(t, "us-east-1", entry.Region)
			assert.Equal(t, int64(1), entry.UseCount)
			assert.WithinDuration(t, time.Now(), entry.CreatedAt, time.Second)
			assert.WithinDuration(t, time.Now(), entry.LastUsed, time.Second)

			// Use the session again and check updated metadata
			_, err = cache.GetSession("us-east-1", "test-key", "test-secret")
			assert.NoError(t, err)

			cache.RLock()
			entry = cache.Sessions[key]
			cache.RUnlock()

			assert.Equal(t, int64(2), entry.UseCount)
			assert.WithinDuration(t, time.Now(), entry.LastUsed, time.Second)
		}
	})

	t.Run("Cleanup", func(t *testing.T) {
		cache := courier.NewSessionCache()
		defer cache.Stop()

		// Create a session
		sess, err := cache.GetSession("us-east-1", "test-key", "test-secret")
		assert.NoError(t, err)
		assert.NotNil(t, sess)

		key := "test-key:us-east-1:test-secret"

		// Manually modify the timestamps to test cleanup
		cache.Lock()
		entry, exists := cache.Sessions[key]
		if assert.True(t, exists) && assert.NotNil(t, entry) {
			entry.CreatedAt = time.Now().Add(-25 * time.Hour) // Older than 24 hours
			entry.LastUsed = time.Now().Add(-7 * time.Hour)   // Not used in last 6 hours
		}
		cache.Unlock()

		// Run cleanup
		cache.Cleanup()

		// Verify session was removed
		cache.RLock()
		_, exists = cache.Sessions[key]
		cache.RUnlock()
		assert.False(t, exists, "session should be removed after cleanup")
	})
}

func TestURLParsing(t *testing.T) {
	// Test invalid URL
	_, err := url.Parse("not-a-url")
	if assert.NoError(t, err) {
		// url.Parse is actually quite permissive, it will parse most strings
		// We need to check the host specifically
		parsed, _ := url.Parse("not-a-url")
		assert.Empty(t, parsed.Host, "Host should be empty for invalid URL")
	}
}

func TestPresignedURL(t *testing.T) {
	t.Run("ValidInput", func(t *testing.T) {
		// Mock AWS credentials
		os.Setenv("AWS_ACCESS_KEY_ID", "test-key")
		os.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
		defer func() {
			os.Unsetenv("AWS_ACCESS_KEY_ID")
			os.Unsetenv("AWS_SECRET_ACCESS_KEY")
		}()

		url, err := courier.PresignedURL(
			"https://my-bucket.s3.amazonaws.com/attachments/file.jpg",
			"test-key",
			"test-secret",
			"us-east-1",
			24,
		)
		assert.NoError(t, err)
		assert.Contains(t, url, "https://")
		assert.Contains(t, url, "my-bucket")
		assert.Contains(t, url, "file.jpg")
		assert.Contains(t, url, "X-Amz-Algorithm")
	})

	t.Run("InvalidInput", func(t *testing.T) {
		testCases := []struct {
			name          string
			link          string
			accessKey     string
			secretKey     string
			region        string
			expiration    int
			expectedError string
		}{
			{
				name:          "Empty Link",
				link:          "",
				accessKey:     "test-key",
				secretKey:     "test-secret",
				region:        "us-east-1",
				expiration:    24,
				expectedError: "empty link provided",
			},
			{
				name:          "Missing Region",
				link:          "https://my-bucket.s3.amazonaws.com/file.jpg",
				accessKey:     "test-key",
				secretKey:     "test-secret",
				region:        "",
				expiration:    24,
				expectedError: "AWS region not provided",
			},
			{
				name:          "Invalid URL Format",
				link:          "not-a-url",
				accessKey:     "test-key",
				secretKey:     "test-secret",
				region:        "us-east-1",
				expiration:    24,
				expectedError: "invalid URL format",
			},
			{
				name:          "Empty Bucket Name",
				link:          "https://.s3.amazonaws.com/file.jpg",
				accessKey:     "test-key",
				secretKey:     "test-secret",
				region:        "us-east-1",
				expiration:    24,
				expectedError: "could not extract bucket name",
			},
			{
				name:          "Invalid URL Scheme",
				link:          "://my-bucket.s3.amazonaws.com/file.jpg",
				accessKey:     "test-key",
				secretKey:     "test-secret",
				region:        "us-east-1",
				expiration:    24,
				expectedError: "invalid URL format",
			},
			{
				name:          "Missing Host",
				link:          "https:///file.jpg",
				accessKey:     "test-key",
				secretKey:     "test-secret",
				region:        "us-east-1",
				expiration:    24,
				expectedError: "invalid URL format",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := courier.PresignedURL(tc.link, tc.accessKey, tc.secretKey, tc.region, tc.expiration)
				if assert.Error(t, err) {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
			})
		}
	})

	t.Run("ExpirationHandling", func(t *testing.T) {
		// Mock AWS credentials
		os.Setenv("AWS_ACCESS_KEY_ID", "test-key")
		os.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
		defer func() {
			os.Unsetenv("AWS_ACCESS_KEY_ID")
			os.Unsetenv("AWS_SECRET_ACCESS_KEY")
		}()

		// Test with zero expiration - should use default
		url, err := courier.PresignedURL(
			"https://my-bucket.s3.amazonaws.com/attachments/file.jpg",
			"test-key",
			"test-secret",
			"us-east-1",
			0,
		)
		assert.NoError(t, err)
		assert.Contains(t, url, "X-Amz-Expires=604800") // 7 days in seconds

		// Test with custom expiration
		url, err = courier.PresignedURL(
			"https://my-bucket.s3.amazonaws.com/attachments/file.jpg",
			"test-key",
			"test-secret",
			"us-east-1",
			1,
		)
		assert.NoError(t, err)
		assert.Contains(t, url, "X-Amz-Expires=3600") // 1 hour in seconds

		// Test with negative expiration - should use default
		url, err = courier.PresignedURL(
			"https://my-bucket.s3.amazonaws.com/attachments/file.jpg",
			"test-key",
			"test-secret",
			"us-east-1",
			-1,
		)
		assert.NoError(t, err)
		assert.Contains(t, url, "X-Amz-Expires=604800") // 7 days in seconds
	})
}

func TestSplitAttachment(t *testing.T) {
	testCases := []struct {
		name         string
		attachment   string
		expectedType string
		expectedURL  string
	}{
		{
			name:         "Valid Attachment",
			attachment:   "image/jpeg:https://example.com/image.jpg",
			expectedType: "image/jpeg",
			expectedURL:  "https://example.com/image.jpg",
		},
		{
			name:         "No Type",
			attachment:   "https://example.com/image.jpg",
			expectedType: "https",
			expectedURL:  "//example.com/image.jpg",
		},
		{
			name:         "Multiple Colons",
			attachment:   "image/jpeg:https://example.com/image:with:colons.jpg",
			expectedType: "image/jpeg",
			expectedURL:  "https://example.com/image:with:colons.jpg",
		},
		{
			name:         "Empty String",
			attachment:   "",
			expectedType: "",
			expectedURL:  "",
		},
		{
			name:         "Only Type",
			attachment:   "image/jpeg:",
			expectedType: "image/jpeg",
			expectedURL:  "",
		},
		{
			name:         "Only URL",
			attachment:   ":https://example.com/image.jpg",
			expectedType: "",
			expectedURL:  "https://example.com/image.jpg",
		},
		{
			name:         "Multiple Types",
			attachment:   "image/jpeg:video/mp4:https://example.com/file",
			expectedType: "image/jpeg",
			expectedURL:  "video/mp4:https://example.com/file",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			attType, attURL := handlers.SplitAttachment(tc.attachment)
			assert.Equal(t, tc.expectedType, attType)
			assert.Equal(t, tc.expectedURL, attURL)
		})
	}
}
