// Package device contains abstractions to handle devices statefully.
package device

import (
	"github.com/diamondburned/go-buttplug"
)

// Device describes a single device.
type Device struct {
	// Name is the device name.
	Name buttplug.DeviceName
	// Index identifies the device.
	Index buttplug.DeviceIndex
	// Messages holds the message types that the device will accept and the
	// features that it supports. Currently, only [buttplug.VibrateCmdMessage],
	// [buttplug.LinearCmdMessage], and [buttplug.RotateCmdMessage] are
	// supported.
	Messages DeviceMessages
}

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
