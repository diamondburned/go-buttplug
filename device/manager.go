package device

import (
	"encoding/json"
	"sort"
	"sync"

	"github.com/diamondburned/go-buttplug"
)

// Manager holds an internal state of devices and does its best to keep it
// updated. A zero-value Manager instance is a valid Manager instance.
type Manager struct {
	// Broadcaster echoes the event channels that Manager listens to. This is
	// useful for guaranteeing that events are only handled after Manager itself
	// is updated.
	buttplug.Broadcaster

	devices     map[buttplug.DeviceIndex]Device
	controllers map[buttplug.DeviceIndex]*Controller
	mutex       sync.RWMutex
	working     sync.WaitGroup
}

// NewManager creates a new device manager.
func NewManager() *Manager {
	return &Manager{}
}

// Devices returns the list of known devices. The list returned is sorted.
func (m *Manager) Devices() []Device {
	m.mutex.RLock()
	devices := make([]Device, 0, len(m.devices))
	for _, device := range m.devices {
		devices = append(devices, device)
	}
	m.mutex.RUnlock()

	sort.Slice(devices, func(i, j int) bool {
		return devices[i].Index < devices[j].Index
	})

	return devices
}

// Controller returns a new device controller for the given device index. If the
// device is not found, then nil is returned.
func (m *Manager) Controller(conn ButtplugConnection, ix buttplug.DeviceIndex) *Controller {
	m.mutex.RLock()
	ctrl, ok := m.controllers[ix]
	m.mutex.RUnlock()

	if ok {
		return ctrl
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	ctrl, ok = m.controllers[ix]
	if ok {
		return ctrl
	}

	device, ok := m.devices[ix]
	if ok {
		if m.controllers == nil {
			m.controllers = make(map[buttplug.DeviceIndex]*Controller, 1)
		}

		ctrl = NewController(conn, device)
		m.controllers[ix] = ctrl
	}

	return nil
}

// Listen listens to the given channel asynchronously. The listening routine
// stops when the channel closes.
func (m *Manager) Listen(ch <-chan buttplug.Message) {
	m.working.Add(1)
	go m.listen(ch)
}

func (m *Manager) listen(ch <-chan buttplug.Message) {
	defer m.working.Done()

	echo := make(chan buttplug.Message)
	defer close(echo)

	m.Broadcaster.Start(echo)

	for ev := range ch {
		m.onMessage(ev)
		echo <- ev
	}
}

func (m *Manager) onMessage(ev buttplug.Message) {
	switch ev := ev.(type) {
	case *buttplug.DeviceAdded:
		m.mutex.Lock()
		m.addDevice(*ev)
		m.mutex.Unlock()

	case *buttplug.DeviceRemoved:
		m.mutex.Lock()
		m.removeDevice(ev.DeviceIndex)
		m.mutex.Unlock()

	case *buttplug.DeviceList:
		m.mutex.Lock()
		m.devices = map[buttplug.DeviceIndex]Device{}
		for _, device := range ev.Devices {
			m.addDevice(buttplug.DeviceAdded{
				DeviceName:     device.DeviceName,
				DeviceIndex:    device.DeviceIndex,
				DeviceMessages: device.DeviceMessages,
			})
		}
		m.mutex.Unlock()
	}
}

func (m *Manager) addDevice(device buttplug.DeviceAdded) {
	var msgs DeviceMessages
	if device.DeviceMessages != nil {
		var ex *buttplug.DeviceMessagesEx
		if err := json.Unmarshal(device.DeviceMessages, &ex); err == nil {
			msgs = convertDeviceMessagesEx(ex)
		}
	}

	m.devices[device.DeviceIndex] = Device{
		Name:     device.DeviceName,
		Index:    device.DeviceIndex,
		Messages: msgs,
	}
}

func (m *Manager) removeDevice(index buttplug.DeviceIndex) {
	delete(m.devices, index)
}

// Wait waits until all the background goroutines of Manager exits.
func (m *Manager) Wait() {
	m.working.Wait()
}
