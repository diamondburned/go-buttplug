package buttplug

// Temporary constants until buttplug.io v4 schema is implemented:

// Actuator type constants.
const (
	ActuatorUnknown   = "Unknown"
	ActuatorVibrate   = "Vibrate"
	ActuatorRotate    = "Rotate"
	ActuatorOscillate = "Oscillate"
	ActuatorConstrict = "Constrict"
	ActuatorInflate   = "Inflate"
	ActuatorPosition  = "Position"
)

// Sensor type constants.
const (
	SensorUnknown  = "Unknown"
	SensorBattery  = "Battery"
	SensorRSSI     = "RSSI"
	SensorButton   = "Button"
	SensorPressure = "Pressure"
)
