// Copyright 2020 the generic-device-plugin authors
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
	"context"
	"crypto/sha1"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	deviceCheckInterval = 5 * time.Second
)

// DeviceSpec defines a device that should be discovered and scheduled.
// DeviceSpec allows multiple host devices to be selected and scheduled fungibly under the same name.
// Furthermore, host devices can be composed into groups of device nodes that should be scheduled
// as an atomic unit.
type DeviceSpec struct {
	// Name is a unique string representing the kind of device this specification describes.
	Name string `json:"name"`
	// Groups is a list of groups of devices that should be scheduled under the same name.
	Groups []*Group `json:"groups"`
}

// Default applies default values for all fields that can be left empty.
func (d *DeviceSpec) Default() {
	for _, g := range d.Groups {
		if g.Count == 0 {
			g.Count = 1
		}
		for _, p := range g.Paths {
			if p.Type == "" {
				p.Type = DevicePathType
			}
			if p.Type == DevicePathType && p.Permissions == "" {
				p.Permissions = "mrw"
			}
		}
	}
}

// Group represents a set of devices that should be grouped and mounted into a container together as one single meta-device.
type Group struct {
	// Paths is the list of devices of which the device group consists.
	// Paths can be globs, in which case each device matched by the path will be schedulable `Count` times.
	// When the paths have differing cardinalities, that is, the globs match different numbers of devices,
	// the cardinality of each path is capped at the lowest cardinality.
	Paths []*Path `json:"paths"`
	// Count specifies how many times this group can be mounted concurrently.
	// When unspecified, Count defaults to 1.
	Count uint `json:"count,omitempty"`
}

// Path represents a file path that should be discovered.
type Path struct {
	// Path is the file path of a device in the host.
	Path string `json:"path"`
	// MountPath is the file path at which the host device should be mounted within the container.
	// When unspecified, MountPath defaults to the Path.
	MountPath string `json:"mountPath,omitempty"`
	// Permissions is the file-system permissions given to the mounted device.
	// Permissions applies only to mounts of type `Device`.
	// This can be one or more of:
	// * r - allows the container to read from the specified device.
	// * w - allows the container to write to the specified device.
	// * m - allows the container to create device files that do not yet exist.
	// When unspecified, Permissions defaults to mrw.
	Permissions string `json:"permissions,omitempty"`
	// ReadOnly specifies whether the path should be mounted read-only.
	// ReadOnly applies only to mounts of type `Mount`.
	ReadOnly bool `json:"readOnly,omitempty"`
	// Type describes what type of file-system node this Path represents and thus how it should be mounted.
	// When unspecified, Type defaults to Device.
	Type PathType `json:"type"`
}

// PathType represents the kinds of file-system nodes that can be scheduled.
type PathType string

const (
	// DevicePathType represents a file-system device node and is mounted as a device.
	DevicePathType PathType = "Device"
	// MountPathType represents an ordinary file-system node and is bind-mounted.
	MountPathType PathType = "Mount"
)

// device wraps the v1.beta1.Device type to add context about
// the device needed by the GenericPlugin.
type device struct {
	v1beta1.Device
	deviceSpecs []*v1beta1.DeviceSpec
	mounts      []*v1beta1.Mount
}

// GenericPlugin is a plugin for generic devices that can:
// * be found using a file path; and
// * mounted and used without special logic.
type GenericPlugin struct {
	ds      *DeviceSpec
	devices map[string]device
	logger  log.Logger
	mu      sync.Mutex

	// metrics
	deviceGauge        prometheus.Gauge
	allocationsCounter prometheus.Counter
}

// NewGenericPlugin creates a new plugin for a generic device.
func NewGenericPlugin(ds *DeviceSpec, pluginDir string, logger log.Logger, reg prometheus.Registerer) Plugin {
	if logger == nil {
		logger = log.NewNopLogger()
	}

	gp := &GenericPlugin{
		ds:      ds,
		devices: make(map[string]device),
		logger:  logger,
		deviceGauge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "generic_device_plugin_devices",
			Help: "The number of devices managed by this device plugin.",
		}),
		allocationsCounter: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "generic_device_plugin_allocations_total",
			Help: "The total number of device allocations made by this device plugin.",
		}),
	}

	if reg != nil {
		reg.MustRegister(gp.deviceGauge, gp.allocationsCounter)
	}

	return NewPlugin(ds.Name, pluginDir, gp, logger, prometheus.WrapRegistererWithPrefix("generic_", reg))
}

func (gp *GenericPlugin) discover() ([]device, error) {
	var devices []device
	var mountPath string
	for _, group := range gp.ds.Groups {
		paths := make([][]string, len(group.Paths))
		var length int
		// Discover all of the devices matching each pattern in the group.
		for i, path := range group.Paths {
			matches, err := filepath.Glob(path.Path)
			if err != nil {
				return nil, err
			}
			sort.Strings(matches)
			paths[i] = matches
			// Keep track of the shortest length in the group.
			if length == 0 || len(matches) < length {
				length = len(matches)
			}
		}
		for i := 0; i < length; i++ {
			for j := uint(0); j < group.Count; j++ {
				h := sha1.New()
				h.Write([]byte(strconv.FormatUint(uint64(j), 10)))
				d := device{
					Device: v1beta1.Device{
						Health: v1beta1.Healthy,
					},
				}
				for k, path := range group.Paths {
					mountPath = path.MountPath
					if mountPath == "" {
						mountPath = paths[k][i]
					}
					switch path.Type {
					case DevicePathType:
						d.deviceSpecs = append(d.deviceSpecs, &v1beta1.DeviceSpec{
							HostPath:      paths[k][i],
							ContainerPath: mountPath,
							Permissions:   path.Permissions,
						})
					case MountPathType:
						d.mounts = append(d.mounts, &v1beta1.Mount{
							HostPath:      paths[k][i],
							ContainerPath: mountPath,
							ReadOnly:      path.ReadOnly,
						})
					}
					h.Write([]byte(paths[k][i]))
				}
				d.ID = fmt.Sprintf("%x", h.Sum(nil))
				devices = append(devices, d)
			}
		}
	}
	return devices, nil
}

// refreshDevices updates the devices available to the
// generic device plugin and returns a boolean indicating
// if everything is OK, i.e. if the devices are the same ones as before.
func (gp *GenericPlugin) refreshDevices() (bool, error) {
	devices, err := gp.discover()
	if err != nil {
		return false, fmt.Errorf("failed to discover devices: %v", err)
	}

	gp.deviceGauge.Set(float64(len(devices)))

	gp.mu.Lock()
	defer gp.mu.Unlock()

	old := gp.devices
	gp.devices = make(map[string]device)

	var equal bool
	// Add the new devices to the map and check
	// if they were in the old map.
	for _, d := range devices {
		gp.devices[d.ID] = d
		if _, ok := old[d.ID]; !ok {
			equal = false
		}
	}
	if !equal {
		return false, nil
	}

	// Check if devices were removed.
	for k := range old {
		if _, ok := gp.devices[k]; !ok {
			return false, nil
		}
	}
	return true, nil
}

// GetDeviceState always returns healthy.
func (gp *GenericPlugin) GetDeviceState(_ string) string {
	return v1beta1.Healthy
}

// Allocate assigns generic devices to a Pod.
func (gp *GenericPlugin) Allocate(_ context.Context, req *v1beta1.AllocateRequest) (*v1beta1.AllocateResponse, error) {
	gp.mu.Lock()
	defer gp.mu.Unlock()
	res := &v1beta1.AllocateResponse{
		ContainerResponses: make([]*v1beta1.ContainerAllocateResponse, 0, len(req.ContainerRequests)),
	}
	for _, r := range req.ContainerRequests {
		resp := new(v1beta1.ContainerAllocateResponse)
		// Add all requested devices to to response.
		for _, id := range r.DevicesIDs {
			d, ok := gp.devices[id]
			if !ok {
				return nil, fmt.Errorf("requested device does not exist %q", id)
			}
			if d.Health != v1beta1.Healthy {
				return nil, fmt.Errorf("requested device is not healthy %q", id)
			}
			resp.Devices = append(resp.Devices, d.deviceSpecs...)
			resp.Mounts = append(resp.Mounts, d.mounts...)
		}
		res.ContainerResponses = append(res.ContainerResponses, resp)
	}
	gp.allocationsCounter.Add(float64(len(res.ContainerResponses)))
	return res, nil
}

// GetDevicePluginOptions always returns an empty response.
func (gp *GenericPlugin) GetDevicePluginOptions(_ context.Context, _ *v1beta1.Empty) (*v1beta1.DevicePluginOptions, error) {
	return &v1beta1.DevicePluginOptions{}, nil
}

// ListAndWatch lists all devices and then refreshes every deviceCheckInterval.
func (gp *GenericPlugin) ListAndWatch(_ *v1beta1.Empty, stream v1beta1.DevicePlugin_ListAndWatchServer) error {
	level.Info(gp.logger).Log("msg", "starting listwatch")
	if _, err := gp.refreshDevices(); err != nil {
		return err
	}
	ok := false
	var err error
	for {
		if !ok {
			res := new(v1beta1.ListAndWatchResponse)
			for _, dev := range gp.devices {
				res.Devices = append(res.Devices, &v1beta1.Device{ID: dev.ID, Health: dev.Health})
			}
			if err := stream.Send(res); err != nil {
				return err
			}
		}
		<-time.After(deviceCheckInterval)
		ok, err = gp.refreshDevices()
		if err != nil {
			return err
		}
	}
}

// PreStartContainer always returns an empty response.
func (gp *GenericPlugin) PreStartContainer(_ context.Context, _ *v1beta1.PreStartContainerRequest) (*v1beta1.PreStartContainerResponse, error) {
	return &v1beta1.PreStartContainerResponse{}, nil
}

// GetPreferredAllocation always returns an empty response.
func (gp *GenericPlugin) GetPreferredAllocation(context.Context, *v1beta1.PreferredAllocationRequest) (*v1beta1.PreferredAllocationResponse, error) {
	return &v1beta1.PreferredAllocationResponse{}, nil
}
