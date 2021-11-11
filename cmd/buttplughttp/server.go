package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/diamondburned/go-buttplug"
	"github.com/diamondburned/go-buttplug/device"
	"github.com/go-chi/chi"
	"github.com/gorilla/schema"
)

type server struct {
	*chi.Mux
	conn    *buttplug.Websocket
	manager *device.Manager
}

func newServer(w *buttplug.Websocket, m *device.Manager) http.Handler {
	s := &server{
		Mux:     chi.NewMux(),
		conn:    w,
		manager: m,
	}

	s.Get("/devices", s.listDevices)

	s.Route("/device/{id:\\d+}", func(r chi.Router) {
		r.Get("/", s.device)
		r.Get("/battery", s.deviceBattery)
		r.Get("/rssi", s.deviceRSSI)

		r.Group(func(r chi.Router) {
			r.Use(needForm)
			r.Post("/vibrate", s.deviceVibrate)
		})
	})

	return s
}

// controllerFromRequest gets the device's controller from the request. It
// writes the error directly into the given response writer and returns nil if
// the device cannot be found.
func (s *server) controllerFromRequest(w http.ResponseWriter, r *http.Request) *device.Controller {
	id, err := parseDeviceID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return nil
	}

	ctrl := s.manager.Controller(s.conn, id)
	if ctrl == nil {
		writeError(w, http.StatusNotFound, errors.New("device not found"))
		return nil
	}

	return ctrl
}

func (s *server) listDevices(w http.ResponseWriter, r *http.Request) {
	devices := s.manager.Devices()
	json.NewEncoder(w).Encode(devices)
}

func (s *server) device(w http.ResponseWriter, r *http.Request) {
	ctrl := s.controllerFromRequest(w, r)
	if ctrl == nil {
		return
	}

	json.NewEncoder(w).Encode(ctrl.Device)
}

func (s *server) deviceBattery(w http.ResponseWriter, r *http.Request) {
	ctrl := s.controllerFromRequest(w, r)
	if ctrl == nil {
		return
	}

	b, err := ctrl.Battery()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	type response struct {
		BatteryLevel float64
	}

	json.NewEncoder(w).Encode(response{
		BatteryLevel: b,
	})
}

func (s *server) deviceRSSI(w http.ResponseWriter, r *http.Request) {
	ctrl := s.controllerFromRequest(w, r)
	if ctrl == nil {
		return
	}

	f, err := ctrl.RSSILevel()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	type response struct {
		RSSILevel float64
	}

	json.NewEncoder(w).Encode(response{
		RSSILevel: f,
	})
}

var formDecoder = schema.NewDecoder()

func (s *server) deviceVibrate(w http.ResponseWriter, r *http.Request) {
	ctrl := s.controllerFromRequest(w, r)
	if ctrl == nil {
		return
	}

	var form struct {
		Motor    int     `json:"motor"` // default 0
		Strength float64 `json:"strength,required"`
	}

	if err := formDecoder.Decode(&form, r.Form); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	err := ctrl.Vibrate(map[int]float64{
		form.Motor: form.Strength,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
}

func parseDeviceID(r *http.Request) (buttplug.DeviceIndex, error) {
	str := chi.URLParam(r, "id")
	i, err := strconv.Atoi(str)
	return buttplug.DeviceIndex(i), err
}

func needForm(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type jsonError struct {
	Error string
}

func writeError(w http.ResponseWriter, code int, err error) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(jsonError{
		Error: err.Error(),
	})
}
