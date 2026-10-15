package rapidpro

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyaruka/courier"
	"github.com/stretchr/testify/assert"
)

const concurrentLookups = 20

// blockingLoader returns a load function that counts its calls, signals when it starts, and blocks until released.
func blockingLoader(started chan<- struct{}, release <-chan struct{}, calls *int32, channel *DBChannel, err error) func(context.Context) (*DBChannel, error) {
	return func(context.Context) (*DBChannel, error) {
		atomic.AddInt32(calls, 1)
		started <- struct{}{}
		<-release
		return channel, err
	}
}

func runConcurrentLookups(key string, load func(context.Context) (*DBChannel, error), started <-chan struct{}, release chan<- struct{}) ([]*DBChannel, []error) {
	channels := make([]*DBChannel, concurrentLookups)
	errs := make([]error, concurrentLookups)

	var wg sync.WaitGroup
	lookup := func(i int) {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		channels[i], errs[i] = loadChannelOnce(ctx, key, load)
	}

	wg.Add(concurrentLookups)
	go lookup(0)
	<-started

	for i := 1; i < concurrentLookups; i++ {
		go lookup(i)
	}

	// give the other lookups time to join the in-flight load before it completes
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	return channels, errs
}

func TestLoadChannelOnceSharesNotFoundWithConcurrentCallers(t *testing.T) {
	started := make(chan struct{}, concurrentLookups)
	release := make(chan struct{})
	var calls int32

	load := blockingLoader(started, release, &calls, nil, courier.ErrChannelNotFound)
	channels, errs := runConcurrentLookups(channelLoadKey(courier.ChannelType("IG"), "not-found-address"), load, started, release)

	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
	for i := range errs {
		assert.Equal(t, courier.ErrChannelNotFound, errs[i])
		assert.Nil(t, channels[i])
	}
}

func TestLoadChannelOnceSharesLoadedChannelWithConcurrentCallers(t *testing.T) {
	started := make(chan struct{}, concurrentLookups)
	release := make(chan struct{})
	var calls int32
	loaded := &DBChannel{ChannelType_: courier.ChannelType("IG")}

	load := blockingLoader(started, release, &calls, loaded, nil)
	channels, errs := runConcurrentLookups(channelLoadKey(courier.ChannelType("IG"), "found-address"), load, started, release)

	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
	for i := range errs {
		assert.NoError(t, errs[i])
		assert.Same(t, loaded, channels[i])
	}
}

func TestLoadChannelOnceStopsWaitingWhenContextIsDone(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	var calls int32

	load := blockingLoader(started, release, &calls, nil, courier.ErrChannelNotFound)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	begin := time.Now()
	channel, err := loadChannelOnce(ctx, channelLoadKey(courier.ChannelType("IG"), "slow-address"), load)

	assert.Equal(t, context.DeadlineExceeded, err)
	assert.Nil(t, channel)
	assert.Less(t, time.Since(begin), time.Second)
}

func TestLoadChannelOnceScopesLoadsByChannelType(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls int32

	load := blockingLoader(started, release, &calls, nil, courier.ErrChannelWrongType)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	for _, channelType := range []courier.ChannelType{"IG", "FBA"} {
		go func(channelType courier.ChannelType) {
			defer wg.Done()
			_, err := loadChannelOnce(ctx, channelLoadKey(channelType, "shared-address"), load)
			assert.Equal(t, courier.ErrChannelWrongType, err)
		}(channelType)
	}

	<-started
	<-started
	close(release)
	wg.Wait()

	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
}
