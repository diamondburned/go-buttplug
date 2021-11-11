# buttplughttp

An example application using the buttplug API that exposes a REST API to
control devices.

## Endpoints

The API will always return either nothing or a JSON object indicating success.
When it returns nothing, 204 is used.

- `GET /api/devices` lists known devices.
- `GET /api/device/n` shows the information for the device with index `n` (where `n`
  is a number).
- `GET /api/device/n/rssi` returns the signal level for the device.
- `GET /api/device/n/battery` returns the battery level for the device.
- `POST /api/device/n/vibrate?strength=1&motor=0` starts vibrating the device.
	- `?strength` is required; its range is 0.0 to 1.0.
	- `?motor` is optional; it indicates the motor number to start.
