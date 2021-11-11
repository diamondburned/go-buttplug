package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"

	"github.com/diamondburned/go-buttplug"
	"github.com/diamondburned/go-buttplug/device"
	"github.com/diamondburned/go-buttplug/intiface"
	"github.com/go-chi/chi"
	"github.com/pkg/errors"
)

var (
	wsPort      = 20000
	httpAddr    = "localhost:8080"
	intifaceCLI = "intiface-cli"
)

func main() {
	flag.IntVar(&wsPort, "ws-port", wsPort, "websocket port to start from")
	flag.StringVar(&httpAddr, "http-addr", httpAddr, "http address to listen to")
	flag.StringVar(&intifaceCLI, "exec", intifaceCLI, "command to execute if no --ws-addr")
	flag.Parse()

	if err := run(); err != nil {
		log.Fatalln(err)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	ws := intiface.NewWebsocket(wsPort, intifaceCLI)
	broadcaster := buttplug.NewBroadcaster()

	manager := device.NewManager()
	manager.Listen(broadcaster.Listen())

	msgCh := broadcaster.Listen()

	// Start connecting and broadcasting messages at the same time.
	broadcaster.Start(ws.Open(ctx))

	httpErr := make(chan error)
	go func() {
		server := newServer(ws.Websocket, manager)

		mux := chi.NewMux()
		mux.Mount("/api", server)

		err := serveHTTP(ctx, httpAddr, mux)

		select {
		case httpErr <- err:
		case <-ctx.Done():
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-httpErr:
			return errors.Wrap(err, "HTTP error")
		case msg := <-msgCh:
			switch msg := msg.(type) {
			case *buttplug.ServerInfo:
				// Server is ready. Start scanning and ask for the list of
				// devices. The device manager will pick up the device messages
				// for us.
				ws.Send(ctx,
					&buttplug.StartScanning{},
					&buttplug.RequestDeviceList{},
				)
			case *buttplug.DeviceList:
				for _, device := range msg.Devices {
					log.Printf("listed device %s (index %d)", device.DeviceName, device.DeviceIndex)
				}
			case *buttplug.DeviceAdded:
				log.Printf("added device %s (index %d)", msg.DeviceName, msg.DeviceIndex)
			case *buttplug.DeviceRemoved:
				log.Println("removed device", msg.DeviceIndex)
			case error:
				log.Println("buttplug error:", msg)
			}
		}
	}
}

func serveHTTP(ctx context.Context, addr string, h http.Handler) error {
	server := http.Server{
		Addr:    addr,
		Handler: h,
	}

	go func() {
		<-ctx.Done()
		server.Shutdown(ctx)
	}()

	log.Print("starting HTTP server at http://", addr)

	if err := server.ListenAndServe(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
	return nil
}
