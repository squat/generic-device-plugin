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

package main

import (
	"fmt"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/mitchellh/mapstructure"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"github.com/squat/generic-device-plugin/deviceplugin"
)

const defaultDomain = "squat.ai"

// initConfig defines config flags, config file, and envs
func initConfig() error {
	cfgFile := flag.String("config", "", "Path to the config file.")
	flag.String("domain", defaultDomain, "The domain to use when when declaring devices.")
	flag.StringArray("device", nil, `The devices to expose. This flag can be repeated to specify multiple device types.
Multiple paths can be given for each type. Paths can be globs.
Should be provided in the form:
{"name": "<name>", "groups": [(device definitions)], "count": <count>}]}
The device definition can be either a path to a device file or a USB device. You cannot define both in the same group.
For device files, use something like: {"paths": [{"path": "<path-1>", "mountPath": "<mount-path-1>"},{"path": "<path-2>", "mountPath": "<mount-path-2>"}]}
For USB devices, use something like: {"usb": [{"vendor": "1209", "product": "000F"}, {"vendor": "1209", "product": "000F", "serial": "00000001"}]}
For example, to expose serial devices with different names: {"name": "serial", "groups": [{"paths": [{"path": "/dev/ttyUSB*"}]}, {"paths": [{"path": "/dev/ttyACM*"}]}]}
The device flag can specify lists of devices that should be grouped and mounted into a container together as one single meta-device.
For example, to allocate and mount an audio capture device: {"name": "capture", "groups": [{"paths": [{"path": "/dev/snd/pcmC0D0c"}, {"path": "/dev/snd/controlC0"}]}]}
For example, to expose a CH340 serial converter: {"name": "ch340", "groups": [{"usb": [{"vendor": "1a86", "product": "7523"}]}]}
A "count" can be specified to allow a discovered device group to be scheduled multiple times.
For example, to permit allocation of the FUSE device 10 times: {"name": "fuse", "groups": [{"count": 10, "paths": [{"path": "/dev/fuse"}]}]}
Note: if omitted, "count" is assumed to be 1`)
	flag.String("plugin-directory", v1beta1.DevicePluginPath, "The directory in which to create plugin sockets.")
	flag.String("log-level", logLevelInfo, fmt.Sprintf("Log level to use. Possible values: %s", availableLogLevels))
	flag.String("listen", ":8080", "The address at which to listen for health and metrics.")
	flag.Bool("version", false, "Print version and exit")

	flag.Parse()
	if err := viper.BindPFlags(flag.CommandLine); err != nil {
		return fmt.Errorf("failed to bind config: %w", err)
	}

	if *cfgFile != "" {
		viper.SetConfigFile(*cfgFile)
	} else {
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
		viper.AddConfigPath("/etc/generic-device-plugin/")
		viper.AddConfigPath(".")
	}

	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Config file not found; ignore error
		} else {
			// Config file was found but another error was produced
			return fmt.Errorf("failed to read config file: %w", err)
		}
	}

	viper.RegisterAlias("devices", "device")

	return nil
}

// getConfiguredDevices returns a list of configured devices
func getConfiguredDevices() ([]*deviceplugin.DeviceSpec, error) {
	switch raw := viper.Get("device").(type) {
	case []string:
		// Assign deviceSpecs from flag
		deviceSpecs := make([]*deviceplugin.DeviceSpec, len(raw))
		for i, data := range raw {
			if err := yaml.Unmarshal([]byte(data), &deviceSpecs[i]); err != nil {
				return nil, fmt.Errorf("failed to parse device %q: %w", data, err)
			}
		}
		return deviceSpecs, nil
	case []interface{}:
		// Assign deviceSpecs from config
		deviceSpecs := make([]*deviceplugin.DeviceSpec, len(raw))
		for i, data := range raw {
			decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
				Result:  &deviceSpecs[i],
				TagName: "json",
				DecodeHook: mapstructure.ComposeDecodeHookFunc(
					deviceplugin.ToUSBIDHookFunc,
				),
			})
			if err != nil {
				return nil, err
			}

			if err := decoder.Decode(data); err != nil {
				return nil, fmt.Errorf("failed to decode device data %q: %w", data, err)
			}
		}
		return deviceSpecs, nil
	default:
		return nil, fmt.Errorf("failed to decode devices: unexpected type: %T", raw)
	}
}
