package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/lmittmann/tint"
	"golang.org/x/sync/errgroup"
	"libdb.so/go-buttplug"
	"libdb.so/go-buttplug/schema/ptr"
	"libdb.so/go-buttplug/schema/v3"
)

var (
	addr        = "ws://localhost:12345"
	setLevel    = 1.0
	pollSensors = 10 * time.Second
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
	flag.Float64Var(&setLevel, "level", setLevel, "vibration level from 0.0 to 1.0")
	flag.DurationVar(&pollSensors, "poll-sensors", pollSensors, "sensor polling frequency")

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

	if err := run(context.Background()); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	errg, ctx := errgroup.WithContext(ctx)
	defer errg.Wait()

	s := &Session{
		wg: errg,
		ws: buttplug.NewWebsocket(addr, nil),
	}

	// Start polling all sensors and reporting them periodically:
	s.startPollingSensors(ctx)

	// Start vibrating all devices as soon as the connection is ready:
	s.startVibratingAll(ctx, setLevel)

	// Note that the connection doesn't need to be started before anything was
	// sent. Start's internal loop is smart enough to queue messages until the
	// connection is established.
	s.startConnection(ctx)

	if err := errg.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
}

type Session struct {
	wg *errgroup.Group
	ws *buttplug.Websocket
}

func (s *Session) startConnection(ctx context.Context) {
	s.wg.Go(func() error {
		return s.ws.Start(ctx)
	})
}

func (s *Session) startPollingSensors(ctx context.Context) {
	type sensorsKey struct {
		deviceIndex schema.DeviceIndex
		sensorIndex int
	}

	msgs, cancel := s.ws.MessageChannel(ctx)
	s.wg.Go(func() error {
		defer cancel()

		sensors := map[sensorsKey]schema.SensorReadCmdItem{}
		tick := time.Tick(pollSensors)
		poll := make(chan struct{}, 1)

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()

			case msg := <-msgs:
				switch msg := msg.(type) {
				case *schema.DeviceListMessage:
					clear(sensors)
					for _, d := range msg.Devices {
						for i, sensor := range d.DeviceMessages.SensorReadCmd {
							sensors[sensorsKey{
								deviceIndex: d.DeviceIndex,
								sensorIndex: i,
							}] = sensor
						}
					}

					select {
					case poll <- struct{}{}:
					default:
					}

				case *schema.SensorReadingMessage:
					sensor, ok := sensors[sensorsKey{
						deviceIndex: msg.DeviceIndex,
						sensorIndex: msg.SensorIndex,
					}]
					if !ok {
						slog.WarnContext(ctx,
							"received sensor reading for unknown sensor",
							"device_index", msg.DeviceIndex,
							"sensor_index", msg.SensorIndex)
						continue
					}

					slog.InfoContext(ctx,
						"sensor reading",
						"device_index", msg.DeviceIndex,
						"sensor_index", msg.SensorIndex,
						"sensor_type", sensor.SensorType,
						"desc", sensor.FeatureDescriptor,
						"data", msg.Data)
				}

			case <-tick:
				select {
				case poll <- struct{}{}:
				default:
				}

			case <-poll:
				for k, sensor := range sensors {
					_, err := s.ws.Send(ctx, &schema.SensorReadCmdMessage{
						DeviceIndex: k.deviceIndex,
						SensorIndex: k.sensorIndex,
						SensorType:  sensor.SensorType,
					})
					if err != nil {
						slog.WarnContext(ctx,
							"failed to request sensor read",
							"device_index", k.deviceIndex,
							"sensor_index", k.sensorIndex,
							"sensor_type", sensor.SensorType,
							"error", err)
					}
				}
			}
		}
	})
}

func (s *Session) startVibratingAll(ctx context.Context, setLevel float64) {
	s.wg.Go(func() error {
		reply, err := s.ws.SendCommand(ctx, &schema.RequestDeviceListMessage{})
		if err != nil {
			return fmt.Errorf("failed to request device list: %w", err)
		}

		deviceListMsg, ok := reply.(*schema.DeviceListMessage)
		if !ok {
			return fmt.Errorf("expected DeviceListMessage, got %T", reply)
		}

		devices := deviceListMsg.Devices

		vibrators := filterList(devices, func(d schema.DevicesItem) bool {
			return slices.ContainsFunc(d.DeviceMessages.ScalarCmd, func(s schema.ScalarCmdItem) bool {
				return ptr.ValueOrZero(s.ActuatorType) == "Vibrate"
			})
		})

		for _, d := range vibrators {
			slog.InfoContext(ctx,
				"found vibrator device",
				"name", d.DeviceName)

			var scalars []schema.ScalarsItem
			for i, cmd := range d.DeviceMessages.ScalarCmd {
				if ptr.ValueOrZero(cmd.ActuatorType) != "Vibrate" {
					continue
				}

				var scalar float64
				if cmd.StepCount != nil {
					scalar = float64(*cmd.StepCount) * setLevel
				} else {
					scalar = setLevel
				}

				scalars = append(scalars, schema.ScalarsItem{
					Index:        i,
					Scalar:       scalar,
					ActuatorType: *cmd.ActuatorType,
				})
			}

			_, err := s.ws.Send(ctx, &schema.ScalarCmdMessage{
				DeviceIndex: d.DeviceIndex,
				Scalars:     scalars,
			})
			if err != nil {
				slog.ErrorContext(ctx,
					"failed to vibrate device",
					"index", d.DeviceIndex,
					"error", err)
			}
		}

		return nil
	})
}

func filterList[T any](in []T, fn func(T) bool) []T {
	return slices.DeleteFunc(slices.Clone(in), func(v T) bool { return !fn(v) })
}
