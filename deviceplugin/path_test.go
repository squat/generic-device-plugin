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

	"github.com/squat/generic-device-plugin/absolute"
	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func TestDiscoverPaths(t *testing.T) {
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
		},
		{
			name: "simple",
			ds: &DeviceSpec{
				Name: "simple",
				Groups: []*Group{
					{
						Paths: []*Path{
							{
								Path: "/dev/simple",
							},
						},
					},
				},
			},
			fs: fstest.MapFS{
				"dev/simple": {},
			},
			out: []device{
				{
					deviceSpecs: []*v1beta1.DeviceSpec{
						{
							ContainerPath: "/dev/simple",
							HostPath:      "/dev/simple",
						},
					},
				},
			},
			err: nil,
		},
		{
			name: "multiple",
			ds: &DeviceSpec{
				Name: "serial",
				Groups: []*Group{
					{
						Paths: []*Path{
							{
								Path:      "/dev/ttyUSB*",
								MountPath: "/dev/ttyUSB0",
							},
						},
					},
				},
			},
			fs: fstest.MapFS{
				"dev/ttyUSB0": {},
				"dev/ttyUSB1": {},
				"dev/ttyUSB2": {},
				"dev/ttyUSB3": {},
			},
			out: []device{
				{
					deviceSpecs: []*v1beta1.DeviceSpec{
						{
							ContainerPath: "/dev/ttyUSB0",
							HostPath:      "/dev/ttyUSB0",
						},
					},
				},
				{
					deviceSpecs: []*v1beta1.DeviceSpec{
						{
							ContainerPath: "/dev/ttyUSB0",
							HostPath:      "/dev/ttyUSB1",
						},
					},
				},
				{
					deviceSpecs: []*v1beta1.DeviceSpec{
						{
							ContainerPath: "/dev/ttyUSB0",
							HostPath:      "/dev/ttyUSB2",
						},
					},
				},
				{
					deviceSpecs: []*v1beta1.DeviceSpec{
						{
							ContainerPath: "/dev/ttyUSB0",
							HostPath:      "/dev/ttyUSB3",
						},
					},
				},
			},
			err: nil,
		},
		{
			name: "multiple with mount directory",
			ds: &DeviceSpec{
				Name: "serial",
				Groups: []*Group{
					{
						Paths: []*Path{
							{
								Path:      "/dev/ttyUSB*",
								MountPath: "/dev/serial/",
							},
						},
					},
				},
			},
			fs: fstest.MapFS{
				"dev/ttyUSB0": {},
				"dev/ttyUSB1": {},
				"dev/ttyUSB2": {},
				"dev/ttyUSB3": {},
			},
			out: []device{
				{
					deviceSpecs: []*v1beta1.DeviceSpec{
						{
							ContainerPath: "/dev/serial/ttyUSB0",
							HostPath:      "/dev/ttyUSB0",
						},
					},
				},
				{
					deviceSpecs: []*v1beta1.DeviceSpec{
						{
							ContainerPath: "/dev/serial/ttyUSB1",
							HostPath:      "/dev/ttyUSB1",
						},
					},
				},
				{
					deviceSpecs: []*v1beta1.DeviceSpec{
						{
							ContainerPath: "/dev/serial/ttyUSB2",
							HostPath:      "/dev/ttyUSB2",
						},
					},
				},
				{
					deviceSpecs: []*v1beta1.DeviceSpec{
						{
							ContainerPath: "/dev/serial/ttyUSB3",
							HostPath:      "/dev/ttyUSB3",
						},
					},
				},
			},
			err: nil,
		},
		{
			name: "only one exists",
			ds: &DeviceSpec{
				Name: "only-one-exists",
				Groups: []*Group{
					{
						Paths: []*Path{
							{
								Path: "/dev/does/not/exist",
							},
							{
								Path: "/dev/does/exist",
							},
						},
					},
				},
			},
			fs: fstest.MapFS{
				"dev/does/exist": {},
			},
			err: nil,
		},
		{
			name: "optional paths - some missing",
			ds: &DeviceSpec{
				Name: "serial",
				Groups: []*Group{
					{
						Paths: []*Path{
							{
								Path:     "/dev/ttyS0",
								Optional: true,
							},
							{
								Path:     "/dev/ttyUSB0",
								Optional: true,
							},
							{
								Path:     "/dev/ttyUSB1",
								Optional: true,
							},
						},
					},
				},
			},
			fs: fstest.MapFS{
				"dev/ttyUSB0": {},
			},
			out: []device{
				{
					deviceSpecs: []*v1beta1.DeviceSpec{
						{
							ContainerPath: "/dev/ttyUSB0",
							HostPath:      "/dev/ttyUSB0",
						},
					},
				},
			},
			err: nil,
		},
		{
			name: "optional paths - all present",
			ds: &DeviceSpec{
				Name: "serial",
				Groups: []*Group{
					{
						Paths: []*Path{
							{
								Path:     "/dev/ttyS0",
								Optional: true,
							},
							{
								Path:     "/dev/ttyUSB0",
								Optional: true,
							},
							{
								Path:     "/dev/ttyUSB1",
								Optional: true,
							},
						},
					},
				},
			},
			fs: fstest.MapFS{
				"dev/ttyS0":   {},
				"dev/ttyUSB0": {},
				"dev/ttyUSB1": {},
			},
			out: []device{
				{
					deviceSpecs: []*v1beta1.DeviceSpec{
						{
							ContainerPath: "/dev/ttyS0",
							HostPath:      "/dev/ttyS0",
						},
						{
							ContainerPath: "/dev/ttyUSB0",
							HostPath:      "/dev/ttyUSB0",
						},
						{
							ContainerPath: "/dev/ttyUSB1",
							HostPath:      "/dev/ttyUSB1",
						},
					},
				},
			},
			err: nil,
		},
		{
			name: "optional paths - all missing",
			ds: &DeviceSpec{
				Name: "serial",
				Groups: []*Group{
					{
						Paths: []*Path{
							{
								Path:     "/dev/ttyS0",
								Optional: true,
							},
							{
								Path:     "/dev/ttyUSB0",
								Optional: true,
							},
						},
					},
				},
			},
			fs:  fstest.MapFS{},
			out: []device{},
			err: nil,
		},
		{
			name: "mount directory",
			ds: &DeviceSpec{
				Name: "input",
				Groups: []*Group{
					{
						Paths: []*Path{
							{
								Path: "/dev/input",
								Type: MountPathType,
							},
						},
					},
				},
			},
			fs: fstest.MapFS{
				"dev/input/event0": {},
				"dev/input/event1": {},
				"dev/input/event2": {},
			},
			out: []device{
				{
					mounts: []*v1beta1.Mount{
						{
							ContainerPath: "/dev/input",
							HostPath:      "/dev/input",
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
				ds: tc.ds,
				fs: absolute.New(tc.fs, "/"),
			}

			out, err := p.discoverPath()
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
