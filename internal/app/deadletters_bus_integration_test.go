//go:build integration

package app

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/jobs/outboxrelay"
)

// TestDeadLettersListsAndRedrivesWhatTheBusKept is ADR 0273 from the
// terminal: under the installation's own namespace, the verb lists the letter
// the bus keeps after the outbox's pile, and a redrive named by its stream id
// puts the message back on its stream and empties the pile.
func TestDeadLettersListsAndRedrivesWhatTheBusKept(t *testing.T) {
	ctx := t.Context()

	dsn := migrateDSN(t)
	deadLetterEnv(t, dsn)
	outboxSchema(t, dsn)

	redisContainer, err := tcredis.Run(ctx, redisImage)
	testcontainers.CleanupContainer(t, redisContainer)
	require.NoError(t, err)
	uri, err := redisContainer.ConnectionString(ctx)
	require.NoError(t, err)
	t.Setenv("EVENT_BUS", config.BackendRedis)
	t.Setenv("REDIS_URL", uri)
	t.Setenv("REDIS_KEY_PREFIX", "shop")

	cfg, err := config.Load()
	require.NoError(t, err)
	busCfg := busConfig(cfg)
	opts, err := redis.ParseURL(uri)
	require.NoError(t, err)
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })

	// The letter as the bus writes it, under the namespace the server's bus
	// derives from the same configuration.
	stream := busCfg.StreamName("order.placed")
	letterID, err := client.XAdd(ctx, &redis.XAddArgs{Stream: busCfg.DeadLetterStream(), Values: map[string]any{
		"id": "evt_poison", "name": "order.placed", "occurred_at": "2026-09-30T12:00:00Z",
		"data": `{"order_id":"order_1"}`, "stream": stream, "message_id": "1-0",
		"consumer": "web-1", "deliveries": "3", "dropped_at": "2026-09-30T12:05:00Z",
	}}).Result()
	require.NoError(t, err)

	var out bytes.Buffer
	require.NoError(t, Main([]string{deadLettersCommand}, &out, Options{}))
	report := out.String()
	assert.Contains(t, report, "the pile is EMPTY", "the outbox's pile comes first")
	assert.Contains(t, report, "1 kept message(s)")
	assert.Contains(t, report, letterID+"  order.placed  deliveries=3")
	assert.NotContains(t, report, "order_1", "the payload is withheld")

	out.Reset()
	require.NoError(t, Main([]string{deadLettersCommand, cmdRedrive, letterID, "-" + flagConfirm, letterID},
		&out, Options{}))
	assert.Contains(t, out.String(), "the bus's pile is now EMPTY")

	back, err := client.XRange(ctx, stream, "-", "+").Result()
	require.NoError(t, err)
	require.Len(t, back, 1, "the message is back on its stream")
	assert.Equal(t, "evt_poison", back[0].Values["id"])
	assert.Equal(t, `{"order_id":"order_1"}`, back[0].Values["data"])
	left, err := client.XLen(ctx, busCfg.DeadLetterStream()).Result()
	require.NoError(t, err)
	assert.Zero(t, left)

	// And the server's own assembly hands the relay's alarm the same pile.
	app, closeApp, err := openApplication(ctx, cfg, slog.New(slog.DiscardHandler), errorreport.NewSink(),
		Options{}, publishesOnly)
	require.NoError(t, err)
	defer closeApp()
	pile, err := container.Resolve[outboxrelay.BusPile](app.container, svcBusDeadLetters)
	require.NoError(t, err, "a Redis bus registers its pile for the relay's alarm")
	_, err = client.XAdd(ctx, &redis.XAddArgs{Stream: busCfg.DeadLetterStream(), Values: map[string]any{
		"id": "evt_again", "name": "order.placed", "stream": stream,
	}}).Result()
	require.NoError(t, err)
	seen, err := pile.Read(ctx, 5)
	require.NoError(t, err)
	assert.Equal(t, int64(1), seen.Count, "the server reads the pile the terminal lists")
}
