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
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/go-kit/kit/log/level"
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
	Vendor USBID `json:"vendor"`
	// Product is the USB Product ID of the device to match on.
	Product USBID `json:"product"`
}

// USBID is a representation of a platform or vendor ID under the USB standard (see gousb.ID)
type USBID uint16

// UnmarshalJSON handles incoming standard platform / vendor IDs.
func (id *USBID) UnmarshalJSON(data []byte) error {
	if string(data) == "null" || string(data) == `""` {
		return nil
	}
	*id = USBID(binary.LittleEndian.Uint16(data))
	return nil
}

// String returns a standardised hexadecimal representation of the USBID.
func (id *USBID) String() string {
	return fmt.Sprintf("%04x", int(*id))
}

// usbDevice represents a physical, tangible USB device.
type usbDevice struct {
	// Vendor is the USB Vendor ID of the device.
	Vendor USBID `json:"vendor"`
	// Product is the USB Product ID of the device.
	Product USBID `json:"product"`
	// Bus is the physical USB bus this device is located at.
	Bus uint16 `json:"bus"`
	// BusDevice is the location of the device on the Bus.
	BusDevice uint16 `json:"busdev"`
}

// BusPath returns the platform-correct path to the raw device.
func (dev *usbDevice) BusPath() (path string) {
	return fmt.Sprintf(usbDevBus, dev.Bus, dev.BusDevice)
}

// readFileToUint16 reads the file at the given path, then returns a representation of that file as uint16.
// Ignores newlines.
// Returns an error if the file could not be read, or parsed as uint16.
func readFileToUint16(path string) (out uint16, err error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		// We can't read this file for some reason
		return out, err
	}
	// To be safe, strip out newlines
	datastr := strings.ReplaceAll(string(bytes), "\n", "")

	// then attempt to parse as uint16.
	dAsInt, err := strconv.ParseUint(datastr, 16, 16)
	if err != nil {
		return out, fmt.Errorf("malformed device data %q: %w", datastr, err)
	}
	// Potential for overflowing, but presume we know what we're doing.
	return uint16(dAsInt), nil
}

// queryUSBDeviceCharacteristicsByDirectory scans the given directory for information regarding the given USB device,
// then returns a pointer to a new usbDevice if information is found.
// Safe to presume that result is set if err is nil.
func queryUSBDeviceCharacteristicsByDirectory(dir os.DirEntry) (result *usbDevice, err error) {
	if !dir.IsDir() {
		// There shouldn't be any raw files in this directory, but just in case.
		return result, fmt.Errorf("not a directory")
	}
	fqPath := filepath.Join(usbDevicesDir, dir.Name())
	// Try to find the vendor ID file inside this device - this is a good indication that we're dealing with a device, not a bus.
	vnd, err := readFileToUint16(filepath.Join(fqPath, usbDevicesDirVendorIDFile))
	if err != nil {
		// We can't read the vendor file for some reason, it probably doesn't exist.
		return result, err
	}

	prd, err := readFileToUint16(filepath.Join(fqPath, usbDevicesDirProductIDFile))
	if err != nil {
		return result, err
	}

	// The following two calls shouldn't fail if the above two exist and are readable.
	bus, err := readFileToUint16(filepath.Join(fqPath, usbDevicesDirBusFile))
	if err != nil {
		return result, err
	}
	busLoc, err := readFileToUint16(filepath.Join(fqPath, usbDevicesDirBusDevFile))
	if err != nil {
		return result, err
	}

	res := usbDevice{
		Vendor:    USBID(vnd),
		Product:   USBID(prd),
		Bus:       bus,
		BusDevice: busLoc,
	}
	return &res, nil
}

// enumerateUSBDevices rapidly scans the OS system bus for attached USB devices.
// Pure Go; does not require external linking.
func enumerateUSBDevices(dir string) (specs []usbDevice, err error) {
	allDevs, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Set up a WaitGroup with a buffered channel for results
	var wg sync.WaitGroup
	devs := make(chan *usbDevice)

	// You could also have a shared slice with a mutex guard, but this way is arguably a little more performant.
	for _, dev := range allDevs {
		// Copy the loop variable
		dev := dev

		// Spawn a goroutine to discover the device information
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := queryUSBDeviceCharacteristicsByDirectory(dev)
			if err != nil {
				// do we want to handle errors here?
				return
			}
			// Successful data will get thrown onto the buffered channel for later merging
			devs <- result
		}()
	}

	go func() {
		defer close(devs)
		wg.Wait()
	}()

	// Now unwind the buffer into the results array
	for d := range devs {
		specs = append(specs, *d)
	}

	return
}

// searchUSBDevices returns a subset of the "devices" slice containing only those usbDevices that match the given vendor and product arguments.
func searchUSBDevices(devices *[]usbDevice, vendor USBID, product USBID) (devs []usbDevice, err error) {
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
		usbDevs, err := enumerateUSBDevices(usbDevicesDir)
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
					level.Debug(gp.logger).Log("msg", "USB device match", "usbdevice", fmt.Sprintf("%v:%v", dev.Vendor, dev.Product), "path", match.BusPath())
					paths = append(paths, match.BusPath())
				}
			} else {
				// Should this be a Warn? It's very unusual, that's for sure...
				level.Info(gp.logger).Log("msg", "no USB devices found attached to system")
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
