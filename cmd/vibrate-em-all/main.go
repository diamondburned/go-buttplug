package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	"github.com/diamondburned/go-buttplug"
	"github.com/diamondburned/go-buttplug/device"
	"github.com/lmittmann/tint"
	"golang.org/x/sync/errgroup"
)

var (
	addr  = "ws://localhost:12345"
	level = 1.0
)

const usage = `
Usage:
	vibrate-em-all [flags]

	Connects to the buttplug server at the given address and vibrates all
	connected devices at maximum intensity. When interrupted, stops all
	vibrations and ends the connection gracefully.

Flags:
`

func init() {
	flag.StringVar(&addr, "addr", addr, "buttplug server websocket address")
	flag.Float64Var(&level, "level", level, "vibration level from 0.0 to 1.0")

	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), strings.TrimPrefix(usage, "\n"))
		flag.PrintDefaults()
	}
}

func main() {
	log.SetFlags(0)
	slog.SetLogLoggerLevel(slog.LevelDebug)
	flag.Parse()

	tintHandler := tint.NewHandler(os.Stderr, &tint.Options{
		Level: slog.LevelDebug,
	})
	logger := slog.New(tintHandler)
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := run(ctx); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	errg, ctx := errgroup.WithContext(ctx)
	defer errg.Wait()

	ws := buttplug.NewWebsocket(addr, slog.Default())
	errg.Go(func() error {
		return ws.Start(ctx)
	})

	deviceRegistry := device.NewRegistry(ws, slog.Default())
	errg.Go(func() error {
		return deviceRegistry.Start(ctx, ws)
	})

	errg.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()

			case msg := <-deviceRegistry.Messages():
				switch msg.(type) {
				case *buttplug.WebsocketReset:
					slog.InfoContext(ctx,
						"websocket reset received, all state cleared")

				case *buttplug.DeviceAdded:
				case *buttplug.DeviceList:
					printAllDeviceInfos(ctx, deviceRegistry)
					vibrateAllDevices(ctx, deviceRegistry, level)

				default:
					slog.InfoContext(ctx,
						"received unhandled buttplug message",
						"message", msg)
				}
			}
		}
	})

	return errg.Wait()
}

func vibrateAllDevices(ctx context.Context, deviceRegistry *device.Registry, level float64) {
	slog.InfoContext(ctx,
		"vibrating all devices",
		"level", level)

	for _, i := range deviceRegistry.DeviceIndexes() {
		controller := deviceRegistry.Controller(i)
		if err := controller.SendVibrateAll(ctx, level); err != nil {
			slog.ErrorContext(ctx,
				"failed to vibrate device",
				"device_index", i,
				"error", err)
		}
	}
}

func printAllDeviceInfos(ctx context.Context, deviceRegistry *device.Registry) {
	for _, i := range deviceRegistry.DeviceIndexes() {
		controller := deviceRegistry.Controller(i)
		printDeviceInfo(ctx, controller)
	}
}

func printDeviceInfo(ctx context.Context, controller *device.Controller) {
	battery, err := controller.Battery(ctx)
	if err != nil {
		slog.ErrorContext(ctx,
			"failed to get battery level",
			"device_index", controller.Device().Index,
			"error", err)
		return
	}

	rssi, err := controller.RSSI(ctx)
	if err != nil {
		slog.ErrorContext(ctx,
			"failed to get rssi level",
			"device_index", controller.Device().Index,
			"error", err)
		return
	}

	slog.InfoContext(ctx,
		"reporting device info",
		"device_index", controller.Device().Index,
		"battery_level", battery.String(),
		"rssi_level", rssi.String())
}
