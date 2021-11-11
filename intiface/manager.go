// Package intiface provides a manager for the Intiface CLI.
package intiface

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/diamondburned/go-buttplug/internal/lazytime"
	"github.com/pkg/errors"
)

// cliManager manages the intiface-cli process.
type cliManager struct {
	arg0    string
	argv    []string
	port    int
	handler CLIHandler
}

// EnableConsole, if true, will make the Intiface CLI print to stderr.
var EnableConsole = false

// CLIHandler describes the handler that the Intiface CLI manager accepts.
type CLIHandler interface {
	// OnConnect is called everytime the IntifaceCLI is started. The caller
	// should use this method to start the connection and optionally block until
	// the connection dies.
	OnConnect(address string) bool
	// Log logs the given error.
	Log(err error)
	// Close closes the handler.
	Close()
}

// StartCLI starts managing the Intiface CLI. The given callback will be called
// everytime the CLI starts. The caller should call the Address method inside
// the callback. It should return false if it cannot connect to that address.
func StartCLI(ctx context.Context, port int, h CLIHandler, arg0 string, argv ...string) {
	if port == 0 {
		port = 20000
	}

	m := &cliManager{
		arg0:    arg0,
		argv:    argv,
		port:    port,
		handler: h,
	}
	go manage(ctx, m)
}

func manage(ctx context.Context, m *cliManager) {
	var timer lazytime.Timer
	var cmd *exec.Cmd

	defer func() {
		// Close the handler.
		m.handler.Close()

		if cmd == nil || cmd.Process == nil {
			return
		}

		// Ensure that the client is interrupted.
		cmd.Process.Signal(os.Interrupt)
		// Give it a few milliseconds to exit.
		time.Sleep(250 * time.Millisecond)
		// Kill it.
		cmd.Process.Kill()
	}()

	// Test ports up to 65535.
	for port := m.port; port < 65535; port++ {
		select {
		case <-ctx.Done():
			return
		default:
			// ok
		}

		address := fmt.Sprintf("ws://localhost:%d", port)

		args := []string{"--wsinsecureport", strconv.Itoa(port)}
		args = append(args, m.argv...)

		cmd = exec.CommandContext(ctx, m.arg0, args...)
		if EnableConsole {
			cmd.Stdout = os.Stderr
		}

		if err := cmd.Start(); err != nil {
			m.handler.Log(errors.Wrap(err, "cannot start intiface"))
			return
		}

		// Hold for 250ms to see if the app has died. If it has, retry without
		// waiting for WS.
		timer.Reset(250 * time.Millisecond)
		if err := timer.Wait(ctx); err != nil {
			return
		}

		if !m.handler.OnConnect(address) {
			cmd.Process.Kill()
			continue
		}

		err := cmd.Wait()

		select {
		case <-ctx.Done():
			return
		default:
		}

		if err != nil {
			m.handler.Log(errors.Wrap(err, "intiface process error"))
			continue
		}

		// Don't increment the port, since we've successfully connected with
		// that same port.
		port--
	}

	// TODO: wrap back and retry
	m.handler.Log(errors.New("port exhausted"))
}
