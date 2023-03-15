// Copyright 2023 the generic-device-plugin authors
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
	"fmt"
	"os"
	"strconv"
	"sync"

	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	usbDevicesDir              = "/sys/bus/usb/devices/"
	usbDevicesDirVendorIDFile  = "idVendor"
	usbDevicesDirProductIDFile = "idProduct"
	usbDevicesDirBusFile       = "busnum"
	usbDevicesDirBusDevFile    = "devnum"
	usbDevBus                  = "/dev/bus/usb/%+04d/%+04d"
)

// USBSpec represents a USB device specification that should be discovered.
// A USB device must match exactly on all the given attributes to pass.
type USBSpec struct {
	// Vendor is the USB Vendor ID of the device to match on.
	// (Both of these get mangled to uint16 for processing - but you should use the hexadecimal representation.)
	Vendor usbID `json:"vendor"`
	// Product is the USB Product ID of the device to match on.
	Product usbID `json:"product"`
}

// usbID is a representation of a platform or vendor ID under the USB standard (see gousb.ID)
type usbID uint16

// UnmarshalJSON handles incoming standard platform / vendor IDs.
func (id *usbID) UnmarshalJSON(data []byte) error {
	if string(data) == "null" || string(data) == `""` {
		return nil
	}
	*id = usbID(binary.LittleEndian.Uint16(data))
	return nil
}

// String returns a standardised hexadecimal representation of the usbID.
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

// BusPath returns the platform-correct path to the raw device.
func (dev *usbDevice) BusPath() (path string) {
	return fmt.Sprintf(usbDevBus, dev.Bus, dev.BusDevice)
}

// queryUSBDeviceCharacteristicsByDirectory scans the given directory for information regarding the given USB device,
// then returns a pointer to a new usbDevice if information is found.
func queryUSBDeviceCharacteristicsByDirectory(dir os.DirEntry) (result *usbDevice, err error) {
	if !dir.IsDir() {
		// There shouldn't be any raw files in this directory, but just in case.
		return
	}
	// Try to find the vendor ID file inside this device - this is a good indication that we're dealing with a device, not a bus.
	vnd, err := os.ReadFile(dir.Name() + "/" + usbDevicesDirVendorIDFile)
	if err != nil {
		// We can't read the vendor file for some reason, it probably doesn't exist.
		return
	}
	prd, err := os.ReadFile(dir.Name() + "/" + usbDevicesDirProductIDFile)
	if err != nil {
		return
	}

	// The following two calls shouldn't fail.
	busRaw, err := os.ReadFile(dir.Name() + "/" + usbDevicesDirBusFile)
	if err != nil {
		return result, err
	}
	bus := binary.LittleEndian.Uint16(busRaw)
	busLocRaw, err := os.ReadFile(dir.Name() + "/" + usbDevicesDirBusDevFile)
	if err != nil {
		return result, err
	}
	busLoc := binary.LittleEndian.Uint16(busLocRaw)

	res := usbDevice{
		Vendor:    usbID(binary.LittleEndian.Uint16(vnd)),
		Product:   usbID(binary.LittleEndian.Uint16(prd)),
		Bus:       bus,
		BusDevice: busLoc,
	}
	return &res, nil
}

// enumerateUSBDevices rapidly scans the OS system bus for attached USB devices.
// Pure Go; does not require external linking.
func enumerateUSBDevices() (specs []usbDevice, err error) {
	allDevs, err := os.ReadDir(usbDevicesDir)
	if err != nil {
		return
	}

	var wg sync.WaitGroup
	for i, dev := range allDevs {
		wg.Add(1)

		// Copy the loop variables
		i := i
		dev := dev

		go func() {
			defer wg.Done()
			result, err := queryUSBDeviceCharacteristicsByDirectory(dev)
			if err != nil {
				// do we want to handle errors here?
				return
			}
			if result != nil {
				specs[i] = *result
			}
		}()
	}

	wg.Wait()

	return
}

// searchUSBDevices returns a subset of the "devices" slice containing only those usbDevices that match the given vendor and product arguments.
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
		var paths []string
		usbDevs, err := enumerateUSBDevices()
		if err != nil {
			return devices, err
		}
		for _, dev := range group.USBSpecs {
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
