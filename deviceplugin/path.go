package deviceplugin

import (
	"crypto/sha1"
	"fmt"
	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	"path/filepath"
	"sort"
	"strconv"
)

// Path represents a file path that should be discovered.
type Path struct {
	// Path is the file path of a device in the host.
	Path string `json:"path"`
	// MountPath is the file path at which the host device should be mounted within the container.
	// When unspecified, MountPath defaults to the Path.
	MountPath string `json:"mountPath,omitempty"`
	// Permissions is the file-system permissions given to the mounted device.
	// Permissions apply only to mounts of type `Device`.
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

func (gp *GenericPlugin) discoverPath() ([]device, error) {
	var devices []device
	var mountPath string
	for _, group := range gp.ds.Groups {
		paths := make([][]string, len(group.Paths))
		var length int
		// Discover all the devices matching each pattern in the Paths group.
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
