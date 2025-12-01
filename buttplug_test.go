package buttplug

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
	"github.com/neilotoole/slogt"
	buttplugschema "libdb.so/go-buttplug/schema/v3"
)

func TestSchema(t *testing.T) {
	tests := []struct {
		name   string
		input  jsontext.Value
		expect buttplugschema.Payload
	}{
		{
			name: "RequestServerInfo",
			input: []byte(`[
				{"RequestServerInfo": {
					"Id": 1,
					"ClientName": "madoka",
					"MessageVersion": 3
				}}
			]`),
			expect: buttplugschema.Payload{
				&buttplugschema.RequestServerInfoMessage{
					ID:             1,
					ClientName:     "madoka",
					MessageVersion: 3,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got buttplugschema.Payload
			assert.NoError(t, json.Unmarshal([]byte(tt.input), &got), "Unmarshal should not error")
			assert.Equal(t, tt.expect, got, "Unmarshaled payload does not match expected")

			gotJSON, err := json.Marshal(got)
			assert.NoError(t, err, "Marshal should not error")

			input := tt.input
			assert.NoError(t, input.Compact(), "Input compact should not error")

			assert.Equal(t, input, jsontext.Value(gotJSON), "Marshaled JSON does not match original input")
		})
	}
}

func TestEndToEnd(t *testing.T) {
	tests := []struct {
		name      string
		prog      func(t *testing.T, conn *Websocket, msgs <-chan buttplugschema.Message)
		dontStart bool
	}{
		{
			name: "handshake",
			prog: func(t *testing.T, conn *Websocket, msgs <-chan buttplugschema.Message) {
				for msg := range msgs {
					switch m := msg.(type) {
					case *WebsocketResetMessage:
						t.Logf("Received Reset: %+v", m)
					case *buttplugschema.ServerInfoMessage:
						t.Logf("Received ServerInfo: %+v", m)
						return
					default:
						t.Logf("Received unexpected message: %+v", m)
					}
				}
				t.Error("message channel closed before handshake completed")
			},
		},
		{
			name: "never_listen",
			prog: func(t *testing.T, conn *Websocket, msgs <-chan buttplugschema.Message) {
				time.Sleep(1 * time.Second)
				t.Log("exiting without listening to messages")
			},
		},
		{
			name: "message_channel_lifecycle",
			prog: func(t *testing.T, conn *Websocket, msgs <-chan buttplugschema.Message) {
				ctx, cancel := contextTimeoutTest(t, 5*time.Second)
				defer cancel()

				msgs1, cancel1 := conn.MessageChannel(ctx)
				defer cancel1()

				msgs2, cancel2 := conn.MessageChannel(ctx)
				defer cancel2()

				startButtplugClient(t, conn, ctx)

			msgs2Loop:
				for msg := range msgs2 {
					switch m := msg.(type) {
					case *WebsocketResetMessage:
						t.Logf("[msgs2] Received Reset: %+v", m)

						// immediately cancel the first context. the second
						// channel should still receive ServerInfo.
						cancel1()

					case *buttplugschema.ServerInfoMessage:
						t.Logf("[msgs2] Received ServerInfo: %+v", m)
						break msgs2Loop
					}
				}

				// ensure msgs1 is closed.
			msgs1Loop:
				for {
					select {
					case msg, ok := <-msgs1:
						if ok {
							t.Logf("[msgs1] unexpected message after cancel: %+v", msg)
						} else {
							t.Logf("[msgs1] channel closed as expected")
							break msgs1Loop
						}
					case <-ctx.Done():
						t.Error("timeout waiting for msgs1 channel to close")
						break msgs1Loop
					}
				}
			},
			dontStart: true,
		},
	}

	addrEnv := os.Getenv("BUTTPLUG_TEST_ADDR")
	if addrEnv == "" {
		t.Skip("$BUTTPLUG_TEST_ADDR not set, skipping integration test")
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := slogt.New(t)

			conn := NewWebsocket(addrEnv, &WebsocketOpts{Logger: logger})
			if tt.dontStart {
				tt.prog(t, conn, nil)
			} else {
				msgs, cancel := conn.MessageChannel(t.Context())
				defer cancel()

				ctx, cancel := contextTimeoutTest(t, 10*time.Second)
				startButtplugClient(t, conn, ctx)

				tt.prog(t, conn, msgs)
				cancel()
			}
		})
	}
}

func startButtplugClient(t *testing.T, conn *Websocket, connCtx context.Context) {
	var g sync.WaitGroup
	t.Cleanup(g.Wait)
	g.Go(func() {
		if err := conn.Start(connCtx); err != nil && err != context.Canceled {
			t.Errorf("buttplug.io connection error: %v", err)
		}
	})
}

func contextCancelTest(t *testing.T) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	return ctx, cancel
}

func contextTimeoutTest(t *testing.T, d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(t.Context(), d)
	t.Cleanup(cancel)
	return ctx, cancel
}
