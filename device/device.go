// Package device contains abstractions to handle devices.
package device

import (
	"context"
	"fmt"
	"time"

	"github.com/diamondburned/go-buttplug"
	"github.com/diamondburned/go-buttplug/internal/debounce"
)

// DeviceMessages is a type that holds the supported message types for a device.
type DeviceMessages map[buttplug.MessageType]buttplug.GenericMessageAttributes

func convertDeviceMessagesEx(ex *buttplug.DeviceMessagesEx) DeviceMessages {
	if ex == nil {
		return nil
	}

	msgs := DeviceMessages{}

	if ex.VibrateCmd != nil {
		msgs[buttplug.VibrateCmdMessage] = *ex.VibrateCmd
	}
	if ex.LinearCmd != nil {
		msgs[buttplug.LinearCmdMessage] = *ex.LinearCmd
	}
	if ex.RotateCmd != nil {
		msgs[buttplug.RotateCmdMessage] = *ex.RotateCmd
	}

	return msgs
}

// Device describes a single device.
type Device struct {
	// Name is the device name.
	Name buttplug.DeviceName
	// Index identifies the device.
	Index buttplug.DeviceIndex
	// Messages holds the message types that the device will accept and the
	// features that it supports.
	Messages DeviceMessages
}

// ButtplugConnection describes a connection to the Buttplug (Intiface)
// controller. A *buttplug.Websocket will satisfy this.
type ButtplugConnection interface {
	Command(ctx context.Context, msg buttplug.Message) (buttplug.Message, error)
	Send(ctx context.Context, msgs ...buttplug.Message)
}

var _ ButtplugConnection = (*buttplug.Websocket)(nil)

// Controller binds a device and a websocket together to provide
// abstraction methods for controlling it.
type Controller struct {
	// Device is the device that this controller belongs to.
	Device
	State *ControllerState

	conn ButtplugConnection
	ctx  context.Context

	async bool
}

// ControllerState is the state of a controller. This state should be unique to
// each device, so Controllers that share the same device must share the same
// state.
type ControllerState struct {
	Debounce debounce.Debouncer
}

// NewControllerState initializes a new ControllerState.
func NewControllerState(freq time.Duration) *ControllerState {
	return &ControllerState{
		Debounce: debounce.Debouncer{
			Frequency: freq,
		},
	}
}

// NewController creates a new device controller.
func NewController(conn ButtplugConnection, device Device, state *ControllerState) *Controller {
	return &Controller{
		Device: device,
		State:  state,
		conn:   conn,
		ctx:    context.Background(),
	}
}

// WithAsync returns a new Controller that is fully asynchronous. When that's
// the case, Controller will send commands asynchronously whenever possible. The
// return types for command methods will always be a zero-value. Methods that
// behave differently when async will be explicitly documented.
func (c *Controller) WithAsync() *Controller {
	copy := *c
	copy.async = true
	return &copy
}

// WithoutAsync returns a new Controller that is not asynchronous. It undoes
// WithAsync.
func (c *Controller) WithoutAsync() *Controller {
	copy := *c
	copy.async = false
	return &copy
}

// WithContext returns a new Controller that internally uses the given
// context.
func (c *Controller) WithContext(ctx context.Context) *Controller {
	copy := *c
	copy.ctx = ctx
	return &copy
}

// Battery queries for the battery level. The returned number is between 0 and
// 1.
func (c *Controller) Battery() (float64, error) {
	m, err := c.conn.Command(c.ctx, &buttplug.BatteryLevelCmd{DeviceIndex: c.Index})
	if err != nil {
		return 0, err
	}

	ev, ok := m.(*buttplug.BatteryLevelReading)
	if !ok {
		return 0, fmt.Errorf("unexpected message type %T", m)
	}

	return ev.BatteryLevel, nil
}

// RSSILevel gets the received signal strength indication level, expressed as dB
// gain, typically like [-100:0], where -100 is the number returned.
func (c *Controller) RSSILevel() (float64, error) {
	m, err := c.conn.Command(c.ctx, &buttplug.RSSILevelCmd{DeviceIndex: c.Index})
	if err != nil {
		return 0, err
	}

	ev, ok := m.(*buttplug.RSSILevelReading)
	if !ok {
		return 0, fmt.Errorf("unexpected message type %T", m)
	}

	return ev.RSSILevel, nil
}

// Stop asks the server to stop the device. Stop ca be asynchronous.
func (c *Controller) Stop() error {
	return c.sendAsyncable(false, &buttplug.StopDeviceCmd{DeviceIndex: c.Index})
}

// deviceSpeed is kept equal to VibrateCmd's.
type deviceSpeed = struct {
	Index int     `json:"Index"`
	Speed float64 `json:"Speed"`
}

func newDeviceSpeeds(speeds map[int]float64) []deviceSpeed {
	deviceSpeeds := make([]deviceSpeed, 0, len(speeds))
	for i, speed := range speeds {
		deviceSpeeds = append(deviceSpeeds, deviceSpeed{
			Index: i,
			Speed: speed,
		})
	}
	return deviceSpeeds
}

// VibrationMotors returns the number of vibration motors for the device. 0 is
// returned if the device doesn't support vibration.
func (c *Controller) VibrationMotors() int {
	attrs, ok := c.Messages[buttplug.VibrateCmdMessage]
	if !ok {
		return 0
	}

	if attrs.FeatureCount != nil {
		return int(*attrs.FeatureCount)
	}

	return 0
}

// VibrationSteps returns the number of vibration steps that the device can
// support. The returned list has the length of the number of motors, with each
// item being the step count of each motor. Nil is returned if the device
// doesn't support vibration or if the server doesn't have this information.
func (c *Controller) VibrationSteps() []int {
	attrs, ok := c.Messages[buttplug.VibrateCmdMessage]
	if !ok {
		return nil
	}

	if attrs.StepCount != nil {
		return *attrs.StepCount
	}

	return nil
}

// Vibrate asks the server to start vibrating. Vibrate can be asynchronous.
func (c *Controller) Vibrate(motorSpeeds map[int]float64) error {
	return c.sendAsyncable(true, &buttplug.VibrateCmd{
		DeviceIndex: c.Index,
		Speeds:      newDeviceSpeeds(motorSpeeds),
	})
}

// Vector describes the linear movement of a motor.
type Vector struct {
	// Duration is the movement time.
	Duration time.Duration
	// Position is the target position ranging from 0.0 to 1.0.
	Position float64
}

type linearVector = struct {
	Index    int     `json:"Index"`
	Duration float64 `json:"Duration"`
	Position float64 `json:"Position"`
}

func newLinearVectors(vectors map[int]Vector) []linearVector {
	linearVectors := make([]linearVector, 0, len(vectors))
	for i, vector := range vectors {
		linearVectors = append(linearVectors, linearVector{
			Index:    i,
			Duration: float64(vector.Duration) / float64(time.Millisecond),
			Position: vector.Position,
		})
	}
	return linearVectors
}

// Linear asks the server to linearly move the device over a certain amount of
// time. Linear can be asynchronous.
func (c *Controller) Linear(vectors map[int]Vector) error {
	return c.sendAsyncable(true, &buttplug.LinearCmd{
		DeviceIndex: c.Index,
		Vectors:     newLinearVectors(vectors),
	})
}

// Rotation describes a rotation that rotating motor does.
type Rotation struct {
	// Speed is the rotation speed.
	Speed float64
	// Clockwise is the direction of rotation.
	Clockwise bool
}

type rotation = struct {
	Index     int     `json:"Index"`
	Speed     float64 `json:"Speed"`
	Clockwise bool    `json:"Clockwise"`
}

func newRotations(rotations map[int]Rotation) []rotation {
	rots := make([]rotation, 0, len(rotations))
	for i, rot := range rotations {
		rots = append(rots, rotation{
			Index:     i,
			Speed:     rot.Speed,
			Clockwise: rot.Clockwise,
		})
	}
	return rots
}

// Rotate asks the server to rotate some or all of the device's motors. Rotate
// can be asynchronous.
func (c *Controller) Rotate(rotations map[int]Rotation) error {
	return c.sendAsyncable(true, &buttplug.RotateCmd{
		DeviceIndex: c.Index,
		Rotations:   newRotations(rotations),
	})
}

func (c *Controller) sendAsyncable(canDebounce bool, cmd buttplug.Message) error {
	send := func() error {
		if c.async {
			c.conn.Send(c.ctx, cmd)
			return nil
		} else {
			_, err := c.conn.Command(c.ctx, cmd)
			return err
		}
	}

	if canDebounce && c.State.Debounce.Frequency > 0 {
		// Errors are passed into the channel anyway, so we can just ignore it
		// here.
		c.State.Debounce.Run(func() { send() })
		return nil
	}

	return send()
}
