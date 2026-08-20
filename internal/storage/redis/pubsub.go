package redis

import (
	"context"

	"github.com/redis/go-redis/v9"

	"realtime-engine/internal/storage"
)

// EventBus implements storage.EventBus over Redis Pub/Sub, channel
// "room-events:{roomID}". It propagates room lifecycle events (e.g. a room
// closing) across nodes so no node's local cache goes stale after another
// node's room actor exits — a hook point for a future gateway/proxy layer
// as much as for application logic today.
type EventBus struct {
	rdb *redis.Client
}

func NewEventBus(c *Client) *EventBus {
	return &EventBus{rdb: c.rdb}
}

var _ storage.EventBus = (*EventBus)(nil)

func roomEventsChannel(roomID string) string { return "room-events:" + roomID }

func (b *EventBus) PublishRoomEvent(ctx context.Context, roomID string, payload []byte) error {
	return b.rdb.Publish(ctx, roomEventsChannel(roomID), payload).Err()
}

func (b *EventBus) SubscribeRoomEvents(ctx context.Context, roomID string) (<-chan []byte, func(), error) {
	sub := b.rdb.Subscribe(ctx, roomEventsChannel(roomID))
	if _, err := sub.Receive(ctx); err != nil {
		_ = sub.Close()
		return nil, nil, err
	}

	out := make(chan []byte, 16)
	go func() {
		defer close(out)
		for msg := range sub.Channel() {
			out <- []byte(msg.Payload)
		}
	}()

	unsubscribe := func() { _ = sub.Close() }
	return out, unsubscribe, nil
}
