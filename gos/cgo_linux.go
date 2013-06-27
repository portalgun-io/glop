package gos

// #cgo LDFLAGS: -Llinux/lib -lglop -lX11 -lGL
// #include "linux/include/glop.h"
import "C"

// #cgo LDFLAGS: -L/home/darthur/src/github.com/runningwild/glop/gos/linux/lib -lglop -lX11 -lGL

import (
	"github.com/runningwild/glop/gin"
	"github.com/runningwild/glop/system"
	"unsafe"
)

type linuxSystemObject struct {
	horizon int64
}

var (
	linux_system_object linuxSystemObject
)

// Call after runtime.LockOSThread(), *NOT* in an init function
func (linux *linuxSystemObject) Startup() {
	C.GlopInit()
}

func GetSystemInterface() system.Os {
	return &linux_system_object
}

func (linux *linuxSystemObject) Run() {
	panic("Not implemented on linux")
}

func (linux *linuxSystemObject) Quit() {
	panic("Not implemented on linux")
}

func (linux *linuxSystemObject) CreateWindow(x, y, width, height int) {
	C.GlopCreateWindow(unsafe.Pointer(&(([]byte("linux window"))[0])), C.int(x), C.int(y), C.int(width), C.int(height))
}

func (linux *linuxSystemObject) SwapBuffers() {
	C.GlopSwapBuffers()
}

func (linux *linuxSystemObject) Think() {
	C.GlopThink()
}

func (linux *linuxSystemObject) GetActiveDevices() map[gin.DeviceType][]gin.DeviceIndex {
	return nil
}

// TODO: Make sure that events are given in sorted order (by timestamp)
// TODO: Adjust timestamp on events so that the oldest timestamp is newer than the
//       newest timestemp from the events from the previous call to GetInputEvents
//       Actually that should be in system
func (linux *linuxSystemObject) GetInputEvents() ([]gin.OsEvent, int64) {
	var first_event *C.GlopKeyEvent
	cp := (*unsafe.Pointer)(unsafe.Pointer(&first_event))
	var length C.int
	var horizon C.longlong
	C.GlopGetInputEvents(cp, unsafe.Pointer(&length), unsafe.Pointer(&horizon))
	linux.horizon = int64(horizon)
	c_events := (*[1000]C.GlopKeyEvent)(unsafe.Pointer(first_event))[:length]
	events := make([]gin.OsEvent, length)
	for i := range c_events {
		events[i] = gin.OsEvent{
			KeyId: gin.KeyId{
				Device: gin.DeviceId{
					Index: 5,
					Type:  gin.DeviceTypeKeyboard,
				},
				Index: gin.KeyIndex(c_events[i].index),
			},
			Press_amt: float64(c_events[i].press_amt),
			Timestamp: int64(c_events[i].timestamp) / 1000000,
		}
	}
	return events, linux.horizon
	// return nil, 0
}

func (linux *linuxSystemObject) HideCursor(hide bool) {
}

func (linux *linuxSystemObject) rawCursorToWindowCoords(x, y int) (int, int) {
	wx, wy, _, wdy := linux.GetWindowDims()
	return x - wx, wy + wdy - y
}

func (linux *linuxSystemObject) GetCursorPos() (int, int) {
	var x, y C.int
	C.GlopGetMousePosition(&x, &y)
	return linux.rawCursorToWindowCoords(int(x), int(y))
}

func (linux *linuxSystemObject) GetWindowDims() (int, int, int, int) {
	var x, y, dx, dy C.int
	C.GlopGetWindowDims(&x, &y, &dx, &dy)
	return int(x), int(y), int(dx), int(dy)
}

func (linux *linuxSystemObject) EnableVSync(enable bool) {
	var _enable C.int
	if enable {
		_enable = 1
	}
	C.GlopEnableVSync(_enable)
}

func (linux *linuxSystemObject) HasFocus() bool {
	// TODO: Implement me!
	return true
}
