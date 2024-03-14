package courier

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHealthCheck(t *testing.T) {

	t.Run("All Ok", func(t *testing.T) {
		healthcheck := NewHealthCheck()

		testCheck1 := func() error {
			return nil
		}

		healthcheck.AddCheck("check test1", testCheck1)
		healthcheck.AddCheck("check test2", testCheck1)

		assert.Equal(t, 2, len(healthcheck.ComponentChecks))

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()

		healthcheck.CheckUp(ctx)

		assert.Equal(t, "Ok", healthcheck.HealthStatus.Status)

		hcJSON, err := json.Marshal(healthcheck.HealthStatus)
		assert.NoError(t, err)
		assert.Equal(t,
			"{\"status\":\"Ok\",\"message\":\"All working fine!\",\"details\":{\"check test1\":{\"message\":\"check test1 ok\",\"status\":\"Ok\"},\"check test2\":{\"message\":\"check test2 ok\",\"status\":\"Ok\"}}}",
			string(hcJSON),
		)
	})

	t.Run("One Error", func(t *testing.T) {
		healthcheck := NewHealthCheck()

		testCheck1 := func() error {
			return nil
		}
		testCheck2 := func() error {
			return errors.New("Test Error")
		}

		healthcheck.AddCheck("check test1", testCheck1)
		healthcheck.AddCheck("check test2", testCheck2)

		assert.Equal(t, 2, len(healthcheck.ComponentChecks))

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()

		healthcheck.CheckUp(ctx)

		assert.Equal(t, "Error", healthcheck.HealthStatus.Status)

		hcJSON, err := json.Marshal(healthcheck.HealthStatus)
		assert.NoError(t, err)
		assert.Equal(t, `{"status":"Error","message":"Test Error","details":{"check test1":{"message":"check test1 ok","status":"Ok"},"check test2":{"message":"Test Error","status":"Error"}}}`, string(hcJSON))
	})

	t.Run("One TimedOut", func(t *testing.T) {
		healthcheck := NewHealthCheck()

		testCheck1 := func() error {
			return nil
		}
		testCheck2 := func() error {
			time.Sleep(time.Second * 2)
			return nil
		}

		healthcheck.AddCheck("check test1", testCheck1)
		healthcheck.AddCheck("check test2", testCheck2)

		assert.Equal(t, 2, len(healthcheck.ComponentChecks))

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		healthcheck.CheckUp(ctx)

		assert.Equal(t, "Error", healthcheck.HealthStatus.Status)

		hcJSON, err := json.Marshal(healthcheck.HealthStatus)
		assert.NoError(t, err)
		assert.Equal(t,
			"{\"status\":\"Error\",\"message\":\"check test2 check is timed out\",\"details\":{\"check test1\":{\"message\":\"check test1 ok\",\"status\":\"Ok\"},\"check test2\":{\"message\":\"check test2 check is timed out\",\"status\":\"Timed Out\"}}}",
			string(hcJSON),
		)
	})

}
