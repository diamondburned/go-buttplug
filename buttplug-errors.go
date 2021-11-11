package buttplug

import (
	"encoding/json"
	"fmt"

	"github.com/pkg/errors"
)

// Error makes the Error message implement the error interface.
func (e *Error) Error() string {
	return "server error: " + e.ErrorMessage
}

// DialError is emitted if the Websocket fails to dial.
type DialError struct {
	Err error
}

// Unwrap returns err.Err.
func (err *DialError) Unwrap() error { return err.Err }

// Error implements error.
func (err *DialError) Error() string {
	return fmt.Sprintf("failed to dial: %s", err.Err)
}

// UnknownEventError is an error that's sent on an unknown event.
type UnknownEventError struct {
	Type MessageType
	Data json.RawMessage
}

// Error returns the formatted error; it implements error.
func (err *UnknownEventError) Error() string {
	return fmt.Sprintf("unknown event type %s", err.Type)
}

// VersionMismatchError is returned if the server's version does not match the
// client's.
type VersionMismatchError struct {
	Server int
}

// Error implements error.
func (err VersionMismatchError) Error() string {
	return fmt.Sprintf(
		"message version mismatch, server has %d, client has %d",
		err.Server, Version,
	)
}

// Additional local-only event types.
const (
	InternalErrorMessage MessageType = "buttplug.InternalError"
)

// InternalError is a pseudo-message that is sent over the event channel on
// background errors.
type InternalError struct {
	// ID is 0 if the error is a background event loop error, or non-0 if it's
	// an event associated with a command.
	ID ID
	// Err is the error.
	Err error
	// Fatal is true if a reconnection is needed.
	Fatal bool
}

func newInternalError(err error, wrap string, fatal bool) *InternalError {
	if wrap != "" {
		err = errors.Wrap(err, wrap)
	}
	return &InternalError{Err: err, Fatal: fatal}
}

// Error implements error.
func (e *InternalError) Error() string {
	return "internal error:" + e.Err.Error()
}

// MessageID returns 0.
func (e *InternalError) MessageID() ID { return 0 }

// SetMessageID panics.
func (e *InternalError) SetMessageID(id ID) {
	panic("SetMessageID called on InternalError")
}

// MessageType implements Message.
func (e *InternalError) MessageType() MessageType {
	return InternalErrorMessage
}
