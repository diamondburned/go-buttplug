package device

import (
	"context"
	"fmt"

	"github.com/diamondburned/go-buttplug"
)

// WebsocketSender describes the sender interface that the [buttplug.Websocket]
// implements.
type WebsocketSender interface {
	Send(ctx context.Context, msg buttplug.Message) error
	SendCommand(ctx context.Context, msg buttplug.Message) (buttplug.Message, error)
}

// WebsocketMessageReceiver describes the message receiver interface that the
// [buttplug.Websocket] implements.
type WebsocketMessageReceiver interface {
	Messages() <-chan buttplug.Message
}

// Websocket combines both [WebsocketSender] and [WebsocketMessageReceiver].
type Websocket interface {
	WebsocketSender
	WebsocketMessageReceiver
}

var _ Websocket = (*buttplug.Websocket)(nil)

func sendCommand[T buttplug.Message](ctx context.Context, ws WebsocketSender, msg buttplug.Message) (T, error) {
	m, err := ws.SendCommand(ctx, msg)
	if err != nil {
		var z T
		return z, fmt.Errorf("sending command %s: %w", msg.MessageType(), err)
	}

	rm, ok := m.(T)
	if !ok {
		var z T
		return rm, fmt.Errorf(
			"unexpected message type %T (wanted %T) for command %s",
			m, z, msg.MessageType())
	}

	return rm, nil
}
