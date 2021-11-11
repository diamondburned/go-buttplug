package intiface

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/go-buttplug"
)

var intifaceCLI = "intiface-cli"

func init() {
	if v := os.Getenv("INTIFACE_CLI"); v != "" {
		intifaceCLI = v
	}

	EnableConsole = true
}

func requireCLI(t *testing.T) {
	_, err := exec.LookPath(intifaceCLI)
	if err != nil {
		t.Skip("skipping test since no intiface", intifaceCLI, "found")
	}
}

func TestWebsocket(t *testing.T) {
	requireCLI(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ws := NewWebsocket(0, intifaceCLI)
	ch := ws.Open(ctx)

	for ev := range ch {
		switch ev := ev.(type) {
		case *buttplug.InternalError:
			t.Log("error:", ev.Err)
		case *buttplug.ServerInfo:
			t.Logf("server %q, version %d.%d", ev.ServerName, ev.MajorVersion, ev.MinorVersion)

			// Try pinging synchronously.
			v, err := ws.Command(ctx, &buttplug.RequestDeviceList{})
			if err != nil {
				t.Error("cannot ping:", err)
			}
			if _, ok := v.(*buttplug.DeviceList); !ok {
				t.Errorf("reply is not of type *DeviceList, but %#v", v)
			}

			cancel()
		default:
			t.Log("got", ev.MessageType())
		}
	}

	if err := ws.LastError(); err != nil {
		t.Error(err)
	}
}

func TestMissing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ws := NewWebsocket(0, "random garbage fhdakjfdhsjklfhkfhdsklasklfhcasfhf")
	ch := ws.Open(ctx)

	// Just spin. The loop should exit.
	for range ch {
	}

	err := ws.LastError()
	if err == nil {
		t.Fatal("unexpected graceful exit")
	}
	if !strings.Contains(err.Error(), "executable file not found in $PATH") {
		t.Fatal("unexpected error:", err)
	}
}

func TestPortTaken(t *testing.T) {
	requireCLI(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	done := make(chan error)

	connect := func() {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		ws := NewWebsocket(0, intifaceCLI)

		for ev := range ws.Open(ctx) {
			switch ev.(type) {
			case *buttplug.ServerInfo:
				cancel()
			}
		}

		done <- ws.LastError()
	}

	// Start up 3 ports.
	for i := 0; i < 3; i++ {
		go connect()
	}

	for i := 0; i < 3; i++ {
		if err := <-done; err != nil {
			t.Error("error:", err)
		}
	}
}
