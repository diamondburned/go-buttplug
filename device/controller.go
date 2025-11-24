package device

import (
	"context"
	"time"

	"github.com/diamondburned/go-buttplug"
	"github.com/diamondburned/go-buttplug/internal/debounce"
)

// Controller is a device controller that allows controlling a buttplug device
// using a higher-level wrapper around the [buttplug.Websocket] commands.
type Controller struct {
	device    Device
	debouncer debounce.Debouncer
	conn      WebsocketSender

	bufVibrateSpeeds  []vibrateSpeeds
	bufLinearVectors  []linearVector
	bufRotationMotors []rotationMotor
}

// NewController creates a new device controller for the given device.
// The device must belong to the websocket session represented by conn.
func NewController(conn WebsocketSender, device Device) *Controller {
	return &Controller{}
}

// Device returns the device information that this controller controls.
func (c *Controller) Device() Device {
	return c.device
}

// Battery queries for the battery level. The returned number is between 0 and
// 1.
func (c *Controller) Battery(ctx context.Context) (Battery, error) {
	reply, err := sendCommand[*buttplug.BatteryLevelReading](ctx, c.conn,
		&buttplug.BatteryLevelCmd{DeviceIndex: c.device.Index})
	if err != nil {
		return 0, err
	}
	return Battery(reply.BatteryLevel), nil
}

// RSSI gets the received signal strength indication level.
func (c *Controller) RSSI(ctx context.Context) (RSSI, error) {
	reply, err := sendCommand[*buttplug.RSSILevelReading](ctx, c.conn,
		&buttplug.RSSILevelCmd{DeviceIndex: c.device.Index})
	if err != nil {
		return 0, err
	}
	return RSSI(reply.RSSILevel), nil
}

// Stop asks the server to stop the device. It does not wait for the device to
// actually stop.
func (c *Controller) Stop(ctx context.Context) error {
	return c.conn.Send(ctx, &buttplug.StopDeviceCmd{DeviceIndex: c.device.Index})
}

func (c *Controller) featureCount(msgType buttplug.MessageType) int {
	attrs, ok := c.device.Messages[msgType]
	if !ok || attrs.FeatureCount == nil {
		return 0
	}
	return int(*attrs.FeatureCount)
}

func (c *Controller) stepCount(msgType buttplug.MessageType) []int {
	attrs, ok := c.device.Messages[msgType]
	if !ok || attrs.StepCount == nil {
		return nil
	}
	return *attrs.StepCount
}

// VibrationMotor describes the speed of a vibration motor.
type VibrationMotor struct {
	// Index is the motor index.
	Index int
	// Speed is the motor speed ranging from 0.0 to 1.0.
	Speed float64
}

type vibrateSpeeds = struct {
	Index int     `json:"Index"`
	Speed float64 `json:"Speed"`
}

// SendVibrate asks the server to start vibrating some or all of the device's
// motors.
func (c *Controller) SendVibrate(ctx context.Context, motors ...VibrationMotor) error {
	if c.bufVibrateSpeeds == nil {
		c.bufVibrateSpeeds = make([]vibrateSpeeds, c.VibrationMotors())
	}

	vibrateSpeeds := c.bufVibrateSpeeds[:len(motors)]
	for i, motor := range motors {
		vibrateSpeeds[i].Index = motor.Index
		vibrateSpeeds[i].Speed = motor.Speed
	}

	return c.conn.Send(ctx, &buttplug.VibrateCmd{
		DeviceIndex: c.device.Index,
		Speeds:      vibrateSpeeds,
	})
}

// VibrateAll is a convenience method around [Controller.SendVibrate] that
// vibrates all motors at the given speed.
func (c *Controller) SendVibrateAll(ctx context.Context, speed float64) error {
	if c.bufVibrateSpeeds == nil {
		c.bufVibrateSpeeds = make([]vibrateSpeeds, c.VibrationMotors())
	}

	motorSpeeds := c.bufVibrateSpeeds
	for i := range motorSpeeds {
		motorSpeeds[i].Index = i
		motorSpeeds[i].Speed = speed
	}

	return c.conn.Send(ctx, &buttplug.VibrateCmd{
		DeviceIndex: c.device.Index,
		Speeds:      motorSpeeds,
	})
}

// VibrationMotors returns the number of vibration motors for the device. 0 is
// returned if the device doesn't support vibration.
func (c *Controller) VibrationMotors() int {
	return c.featureCount(buttplug.VibrateCmdMessage)
}

// TODO: figure out what this does.
func (c *Controller) VibrationSteps() []int {
	return c.stepCount(buttplug.VibrateCmdMessage)
}

// LinearMotor describes a linear motor movement.
type LinearMotor struct {
	// Index is the motor index.
	Index int
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

// SendLinear asks the server to move some or all of the device's linear motors.
func (c *Controller) SendLinear(ctx context.Context, motors ...LinearMotor) error {
	if c.bufLinearVectors == nil {
		c.bufLinearVectors = make([]linearVector, c.LinearMotors())
	}

	linearVectors := c.bufLinearVectors[:len(motors)]
	for i, motor := range motors {
		linearVectors[i].Index = motor.Index
		linearVectors[i].Duration = float64(motor.Duration.Milliseconds())
	}

	return c.conn.Send(ctx, &buttplug.LinearCmd{
		DeviceIndex: c.device.Index,
		Vectors:     linearVectors,
	})
}

// LinearMotors returns the number of linear motors for the device. 0 is
// returned if the device doesn't support linear movement.
func (c *Controller) LinearMotors() int {
	return c.featureCount(buttplug.LinearCmdMessage)
}

func (c *Controller) LinearSteps() []int {
	return c.stepCount(buttplug.LinearCmdMessage)
}

// RotationMotor describes a rotation that rotating motor does.
type RotationMotor struct {
	// Index is the motor index.
	Index int
	// Speed is the rotation speed.
	Speed float64
	// Clockwise is the direction of rotation.
	Clockwise bool
}

type rotationMotor = struct {
	Index     int     `json:"Index"`
	Speed     float64 `json:"Speed"`
	Clockwise bool    `json:"Clockwise"`
}

// SendRotate asks the server to rotate some or all of the device's motors.
func (c *Controller) SendRotate(ctx context.Context, motors ...RotationMotor) error {
	if c.bufRotationMotors == nil {
		c.bufRotationMotors = make([]rotationMotor, c.RotationMotors())
	}

	rotations := c.bufRotationMotors[:len(motors)]
	for i, motor := range motors {
		rotations[i].Index = motor.Index
		rotations[i].Speed = motor.Speed
	}

	return c.conn.Send(ctx, &buttplug.RotateCmd{
		DeviceIndex: c.device.Index,
		Rotations:   rotations,
	})
}

// RotationMotors returns the number of rotation motors for the device. 0 is
// returned if the device doesn't support rotation.
func (c *Controller) RotationMotors() int {
	return c.featureCount(buttplug.RotateCmdMessage)
}

func (c *Controller) RotationSteps() []int {
	return c.stepCount(buttplug.RotateCmdMessage)
}
