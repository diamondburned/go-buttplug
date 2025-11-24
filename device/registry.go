package device

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/diamondburned/go-buttplug"
)

// Registry holds all known devices that can be used via controllers as internal
// state. It handles ingesting websocket messages to keep this internal state
// updated.
type Registry struct {
	conn Websocket
	msgs chan buttplug.Message

	mu          sync.RWMutex
	controllers map[buttplug.DeviceIndex]*Controller
}

// NewRegistry creates a new device registry that uses the given websocket
// connection to send commands.
func NewRegistry(conn Websocket, logger *slog.Logger) *Registry {
	return &Registry{
		conn:        conn,
		msgs:        make(chan buttplug.Message, 1),
		controllers: make(map[buttplug.DeviceIndex]*Controller),
	}
}

// DeviceIndexes returns all known device indexes.
func (r *Registry) DeviceIndexes() []buttplug.DeviceIndex {
	r.mu.RLock()
	defer r.mu.RUnlock()

	indexes := make([]buttplug.DeviceIndex, 0, len(r.controllers))
	for _, controller := range r.controllers {
		indexes = append(indexes, controller.device.Index)
	}

	return indexes
}

// Controller returns a controller for the given device index. If the device is
// not found, then nil is returned.
func (r *Registry) Controller(ix buttplug.DeviceIndex) *Controller {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.controllers[ix]
}

// Messages returns a channel that receives all messages from the websocket
// connection given in [NewRegistry]. When [Registry.Start] is called, use this
// method to still receive all messages. If [Registry.Start] is not called, this
// channel will not receive any messages.
func (r *Registry) Messages() <-chan buttplug.Message {
	return r.msgs
}

// Start runs the registry, blocking until the context is done.
// This function will call [Registry.HandleMessage] for each message received
// from the websocket. If the caller prefers to handle messages themselves,
// they'll need to call [Registry.HandleMessage] manually for each message.
func (r *Registry) Start(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case msg := <-r.conn.Messages():
			r.HandleMessage(ctx, msg)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case r.msgs <- msg:
				// ok
			}
		}
	}
}

// HandleMessage handles a message for the registry.
func (r *Registry) HandleMessage(ctx context.Context, msg buttplug.Message) error {
	switch msg := msg.(type) {
	case *buttplug.WebsocketReset:
		r.mu.Lock()
		defer r.mu.Unlock()

		r.controllers = make(map[buttplug.DeviceIndex]*Controller)
		return nil

	case *buttplug.DeviceAdded:
		r.mu.Lock()
		r.onDeviceAdd(*msg)
		r.mu.Unlock()

		return nil

	case *buttplug.DeviceRemoved:
		r.mu.Lock()
		r.onDeviceRemove(*msg)
		r.mu.Unlock()

		return nil

	case *buttplug.DeviceList:
		r.mu.Lock()
		defer r.mu.Unlock()

		for _, device := range msg.Devices {
			r.onDeviceAdd(buttplug.DeviceAdded{
				DeviceName:     device.DeviceName,
				DeviceIndex:    device.DeviceIndex,
				DeviceMessages: device.DeviceMessages,
			})
		}
		return nil

	default:
		return nil
	}
}

func (r *Registry) onDeviceAdd(ev buttplug.DeviceAdded) {
	var msgs DeviceMessages

	if ev.DeviceMessages != nil {
		var ex *buttplug.DeviceMessagesEx
		if err := json.Unmarshal(ev.DeviceMessages, &ex); err == nil {
			msgs = convertDeviceMessagesEx(ex)
		}
	}

	device := Device{
		Name:     ev.DeviceName,
		Index:    ev.DeviceIndex,
		Messages: msgs,
	}

	controller := NewController(r.conn, device)
	r.controllers[ev.DeviceIndex] = controller
}

func (r *Registry) onDeviceRemove(ev buttplug.DeviceRemoved) {
	delete(r.controllers, ev.DeviceIndex)
}
