// Copyright YEAR the generic-device-plugin authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package deviceplugin

import (
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	"os"
	"runtime"
	"strconv"
)

const (
	usbDevicesDir              = "/sys/bus/usb/devices/"
	usbDevicesDirVendorIdFile  = "idVendor"
	usbDevicesDirProductIdFile = "idProduct"
	usbDevicesDirBusFile       = "busnum"
	usbDevicesDirBusDevFile    = "devnum"
	usbDevBus                  = "/dev/bus/usb/%+04d/%+04d"
)

var errPlatformNotSupported error = errors.New("functionality not supported on this platform")

func TestUsbFunctionalityAvailableOnThisPlatform() (err error) {
	if runtime.GOOS != "linux" {
		return errPlatformNotSupported
	}
	return
}

// UsbSpec represents a USB device specification that should be discovered.
// A USB device must match exactly on all the given attributes to pass.
type UsbSpec struct {
	// Vendor is the USB Vendor ID of the device to match on.
	// (Both of these get mangled to uint16 for processing - but you should use the hexadecimal representation.)
	Vendor usbID `json:"vendor"`
	// Product is the USB Product ID of the device to match on.
	Product usbID `json:"product"`
}

type usbID uint16

func (id *usbID) UnmarshalJSON(data []byte) error {
	if string(data) == "null" || string(data) == `""` {
		return nil
	}
	*id = usbID(binary.LittleEndian.Uint16(data))
	return nil
}

func (id *usbID) String() string {
	return fmt.Sprintf("%04x", int(*id))
}

// usbDevice represents a physical, tangible USB device.
type usbDevice struct {
	// Vendor is the USB Vendor ID of the device.
	Vendor usbID `json:"vendor"`
	// Product is the USB Product ID of the device.
	Product usbID `json:"product"`
	// Bus is the physical USB bus this device is located at.
	Bus uint16 `json:"bus"`
	// BusDevice is the location of the device on the Bus.
	BusDevice uint16 `json:"busdev"`
}

func (dev *usbDevice) BusPath() (path string) {
	return fmt.Sprintf(usbDevBus, dev.Bus, dev.BusDevice)
}

func enumerateUSBDevices() (specs []usbDevice, err error) {
	allDevs, err := os.ReadDir(usbDevicesDir)
	if err != nil {
		return
	}
	for _, dev := range allDevs {
		if !dev.IsDir() {
			// There shouldn't be any raw files in this directory, but just in case.
			continue
		}
		// Try to find the vendor ID file inside this device - this is a good indication that we're dealing with a device, not a bus.
		vnd, err := os.ReadFile(dev.Name() + "/" + usbDevicesDirVendorIdFile)
		if err != nil {
			// We can't read the vendor file for some reason, it probably doesn't exist.
			continue
		}
		prd, err := os.ReadFile(dev.Name() + "/" + usbDevicesDirProductIdFile)
		if err != nil {
			continue
		}

		// The following two calls shouldn't fail.
		busRaw, err := os.ReadFile(dev.Name() + "/" + usbDevicesDirBusFile)
		if err != nil {
			return specs, err
		}
		bus := binary.LittleEndian.Uint16(busRaw)
		busLocRaw, err := os.ReadFile(dev.Name() + "/" + usbDevicesDirBusDevFile)
		if err != nil {
			return specs, err
		}
		busLoc := binary.LittleEndian.Uint16(busLocRaw)

		spec := usbDevice{
			Vendor:    usbID(binary.LittleEndian.Uint16(vnd)),
			Product:   usbID(binary.LittleEndian.Uint16(prd)),
			Bus:       bus,
			BusDevice: busLoc,
		}
		specs = append(specs, spec)
	}
	return
}

func searchUSBDevices(devices *[]usbDevice, vendor usbID, product usbID) (devs []usbDevice, err error) {
	for _, dev := range *devices {
		if dev.Vendor == vendor && dev.Product == product {
			devs = append(devs, dev)
		}
	}
	return
}

func (gp *GenericPlugin) discoverUSB() (devices []device, err error) {
	for _, group := range gp.ds.Groups {
		paths := make([]string, len(group.UsbSpecs))
		usbDevs, err := enumerateUSBDevices()
		if err != nil {
			return devices, err
		}
		for _, dev := range group.UsbSpecs {
			matches, err := searchUSBDevices(&usbDevs, dev.Vendor, dev.Product)
			if err != nil {
				return nil, err
			}
			if len(matches) > 0 {
				for _, match := range matches {
					paths = append(paths, match.BusPath())
				}
			}
		}
		if len(paths) > 0 {
			for j := uint(0); j < group.Count; j++ {
				h := sha1.New()
				h.Write([]byte(strconv.FormatUint(uint64(j), 10)))
				d := device{
					Device: v1beta1.Device{
						Health: v1beta1.Healthy,
					},
				}
				for _, path := range paths {
					d.deviceSpecs = append(d.deviceSpecs, &v1beta1.DeviceSpec{
						HostPath:      path,
						ContainerPath: path,
						Permissions:   "rw",
					})
					h.Write([]byte(path))
				}
				d.ID = fmt.Sprintf("%x", h.Sum(nil))
				devices = append(devices, d)
			}
		}
	}
	return devices, nil
}
