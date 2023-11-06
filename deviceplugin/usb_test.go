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
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/go-kit/kit/log"
	"github.com/squat/generic-device-plugin/absolute"
	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func TestDiscoverUSB(t *testing.T) {
	for _, tc := range []struct {
		name string
		ds   *DeviceSpec
		fs   fs.FS
		out  []device
		err  error
	}{
		{
			name: "nil",
			ds:   new(DeviceSpec),
			fs:   fstest.MapFS{},
		},
		{
			name: "simple",
			ds: &DeviceSpec{
				Name: "simple",
				Groups: []*Group{
					{
						USBSpecs: []*USBSpec{
							{
								Vendor:  0x1050,
								Product: 0x0407,
							},
						},
					},
				},
			},
			fs: fstest.MapFS{
				"sys/bus/usb/devices/3-4/idVendor":  {Data: []byte("1050\n")},
				"sys/bus/usb/devices/3-4/idProduct": {Data: []byte("0407\n")},
				"sys/bus/usb/devices/3-4/busnum":    {Data: []byte("3\n")},
				"sys/bus/usb/devices/3-4/devnum":    {Data: []byte("22\n")},
				"sys/bus/usb/devices/3-4/serial":    {Data: []byte("51\n")},
			},
			out: []device{
				{
					deviceSpecs: []*v1beta1.DeviceSpec{
						{
							ContainerPath: "/dev/bus/usb/003/022",
							HostPath:      "/dev/bus/usb/003/022",
						},
					},
				},
			},
			err: nil,
		},
		{
			name: "no-serial",
			ds: &DeviceSpec{
				Name: "no-serial",
				Groups: []*Group{
					{
						USBSpecs: []*USBSpec{
							{
								Vendor:  0x1050,
								Product: 0x0407,
							},
						},
					},
				},
			},
			fs: fstest.MapFS{
				"sys/bus/usb/devices/3-4/idVendor":  {Data: []byte("1050\n")},
				"sys/bus/usb/devices/3-4/idProduct": {Data: []byte("0407\n")},
				"sys/bus/usb/devices/3-4/busnum":    {Data: []byte("3\n")},
				"sys/bus/usb/devices/3-4/devnum":    {Data: []byte("22\n")},
			},
			out: []device{
				{
					deviceSpecs: []*v1beta1.DeviceSpec{
						{
							ContainerPath: "/dev/bus/usb/003/022",
							HostPath:      "/dev/bus/usb/003/022",
						},
					},
				},
			},
			err: nil,
		},
		{
			name: "serial",
			ds: &DeviceSpec{
				Name: "serial",
				Groups: []*Group{
					{
						USBSpecs: []*USBSpec{
							{
								Vendor:  0x1050,
								Product: 0x0407,
								Serial:  "52",
							},
						},
					},
				},
			},
			fs: fstest.MapFS{
				"sys/bus/usb/devices/3-4/idVendor":  {Data: []byte("1050\n")},
				"sys/bus/usb/devices/3-4/idProduct": {Data: []byte("0407\n")},
				"sys/bus/usb/devices/3-4/busnum":    {Data: []byte("3\n")},
				"sys/bus/usb/devices/3-4/devnum":    {Data: []byte("22\n")},
				"sys/bus/usb/devices/3-4/serial":    {Data: []byte("51\n")},
				"sys/bus/usb/devices/4-4/idVendor":  {Data: []byte("1050\n")},
				"sys/bus/usb/devices/4-4/idProduct": {Data: []byte("0407\n")},
				"sys/bus/usb/devices/4-4/busnum":    {Data: []byte("4\n")},
				"sys/bus/usb/devices/4-4/devnum":    {Data: []byte("25\n")},
				"sys/bus/usb/devices/4-4/serial":    {Data: []byte("52\n")},
			},
			out: []device{
				{
					deviceSpecs: []*v1beta1.DeviceSpec{
						{
							ContainerPath: "/dev/bus/usb/004/025",
							HostPath:      "/dev/bus/usb/004/025",
						},
					},
				},
			},
			err: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.ds.Default()
			p := GenericPlugin{
				ds:     tc.ds,
				fs:     absolute.New(tc.fs, "/"),
				logger: log.NewNopLogger(),
			}

			out, err := p.discoverUSB()
			if (err != nil) != (tc.err != nil) {
				t.Errorf("expected error %v; got %v", tc.err, err)
			}
			if len(out) != len(tc.out) {
				t.Errorf("expected %d devices; got %d", len(tc.out), len(out))
				return
			}
			for i := range out {
				if len(out[i].deviceSpecs) != len(tc.out[i].deviceSpecs) {
					t.Errorf("device %d: expected %d deviceSpecs; got %d", i, len(tc.out[i].deviceSpecs), len(out[i].deviceSpecs))
					break
				}
				for j := range out[i].deviceSpecs {
					if out[i].deviceSpecs[j].ContainerPath != tc.out[i].deviceSpecs[j].ContainerPath {
						t.Errorf("device %d, device spec %d: expected container path %q; got %q", i, j, tc.out[i].deviceSpecs[j].ContainerPath, out[i].deviceSpecs[j].ContainerPath)
					}
					if out[i].deviceSpecs[j].HostPath != tc.out[i].deviceSpecs[j].HostPath {
						t.Errorf("device %d, device spec %d: expected host path %q; got %q", i, j, tc.out[i].deviceSpecs[j].HostPath, out[i].deviceSpecs[j].HostPath)
					}
				}
				for j := range out[i].mounts {
					if out[i].mounts[j].ContainerPath != tc.out[i].mounts[j].ContainerPath {
						t.Errorf("device %d, mount %d: expected container path %q; got %q", i, j, tc.out[i].mounts[j].ContainerPath, out[i].mounts[j].ContainerPath)
					}
					if out[i].mounts[j].HostPath != tc.out[i].mounts[j].HostPath {
						t.Errorf("device %d, mount %d: expected host path %q; got %q", i, j, tc.out[i].mounts[j].HostPath, out[i].mounts[j].HostPath)
					}
				}
			}
		})
	}
}
