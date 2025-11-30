package device

import (
	"fmt"
)

// Battery represents a battery level as a percentage (0.0 to 1.0).
type Battery float64

// Float64 converts the Battery to a float64 value.
func (b Battery) Float64() float64 {
	return float64(b)
}

// String formats the Battery as a percentage string.
func (b Battery) String() string {
	return fmt.Sprintf("%.2f%%", b.Float64()*100)
}

// RSSI represents the received signal strength indication level in dB.
// Range: [-100.0 to 0.0].
type RSSI float64

// Float64 converts the RSSI to a float64 value.
func (r RSSI) Float64() float64 {
	return float64(r)
}

// String formats the RSSI as a dB string.
func (r RSSI) String() string {
	return fmt.Sprintf("%.2f dB", r.Float64())
}
