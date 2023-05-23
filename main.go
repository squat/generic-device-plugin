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
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"regexp"
	"runtime"
	"strings"
	"syscall"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/squat/generic-device-plugin/deviceplugin"
	"github.com/squat/generic-device-plugin/version"
)

const (
	logLevelAll   = "all"
	logLevelDebug = "debug"
	logLevelInfo  = "info"
	logLevelWarn  = "warn"
	logLevelError = "error"
	logLevelNone  = "none"
)

var (
	availableLogLevels = strings.Join([]string{
		logLevelAll,
		logLevelDebug,
		logLevelInfo,
		logLevelWarn,
		logLevelError,
		logLevelNone,
	}, ", ")
)

func testUSBFunctionalityAvailableOnThisPlatform() (err error) {
	if runtime.GOOS != "linux" {
		return errors.New("functionality not supported on this platform")
	}
	return
}

// Main is the principal function for the binary, wrapped only by `main` for convenience.
func Main() error {
	if err := initConfig(); err != nil {
		return err
	}

	if viper.GetBool("version") {
		fmt.Println(version.Version)
		return nil
	}

	domain := viper.GetString("domain")
	if errs := validation.IsDNS1123Subdomain(domain); len(errs) > 0 {
		return fmt.Errorf("failed to parse domain %q: %s", domain, strings.Join(errs, ", "))
	}

	deviceTypeFmt := "[a-z0-9][-a-z0-9]*[a-z0-9]"
	deviceTypeRegexp := regexp.MustCompile("^" + deviceTypeFmt + "$")
	var trim string
	var shouldTestUSBAvailable bool
	deviceSpecs, err := getConfiguredDevices()
	if err != nil {
		return err
	}
	for i, dsr := range deviceSpecs {
		// Apply defaults.
		deviceSpecs[i].Default()
		trim = strings.TrimSpace(deviceSpecs[i].Name)
		if !deviceTypeRegexp.MatchString(trim) {
			return fmt.Errorf("failed to parse device %q; device type must match the regular expression %q", dsr.Name, deviceTypeFmt)
		}
		deviceSpecs[i].Name = path.Join(viper.GetString("domain"), trim)
		for j, g := range deviceSpecs[i].Groups {
			if len(g.Paths) > 0 && len(g.USBSpecs) > 0 {
				return fmt.Errorf(
					"failed to parse device %q; cannot define both path and usb at the same time",
					dsr.Name,
				)
			}
			if len(g.USBSpecs) > 0 {
				// Should test USB can be used.
				shouldTestUSBAvailable = true
			}
			for k := range deviceSpecs[i].Groups[j].Paths {
				deviceSpecs[i].Groups[j].Paths[k].Path = strings.TrimSpace(deviceSpecs[i].Groups[j].Paths[k].Path)
				deviceSpecs[i].Groups[j].Paths[k].MountPath = strings.TrimSpace(deviceSpecs[i].Groups[j].Paths[k].MountPath)
			}
		}
	}
	if len(deviceSpecs) == 0 {
		return fmt.Errorf("at least one device must be specified")
	}

	if shouldTestUSBAvailable {
		err := testUSBFunctionalityAvailableOnThisPlatform()
		if err != nil {
			return err
		}
	}

	logger := log.NewJSONLogger(log.NewSyncWriter(os.Stdout))
	logLevel := viper.GetString("log-level")
	switch logLevel {
	case logLevelAll:
		logger = level.NewFilter(logger, level.AllowAll())
	case logLevelDebug:
		logger = level.NewFilter(logger, level.AllowDebug())
	case logLevelInfo:
		logger = level.NewFilter(logger, level.AllowInfo())
	case logLevelWarn:
		logger = level.NewFilter(logger, level.AllowWarn())
	case logLevelError:
		logger = level.NewFilter(logger, level.AllowError())
	case logLevelNone:
		logger = level.NewFilter(logger, level.AllowNone())
	default:
		return fmt.Errorf("log level %v unknown; possible values are: %s", logLevel, availableLogLevels)
	}
	logger = log.With(logger, "ts", log.DefaultTimestampUTC)
	logger = log.With(logger, "caller", log.DefaultCaller)

	r := prometheus.NewRegistry()
	r.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	var g run.Group
	{
		// Run the HTTP server.
		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mux.Handle("/metrics", promhttp.HandlerFor(r, promhttp.HandlerOpts{}))
		listen := viper.GetString("listen")
		l, err := net.Listen("tcp", listen)
		if err != nil {
			return fmt.Errorf("failed to listen on %s: %v", listen, err)
		}

		g.Add(func() error {
			if err := http.Serve(l, mux); err != nil && err != http.ErrServerClosed {
				return fmt.Errorf("server exited unexpectedly: %v", err)
			}
			return nil
		}, func(error) {
			l.Close()
		})
	}

	{
		// Exit gracefully on SIGINT and SIGTERM.
		term := make(chan os.Signal, 1)
		signal.Notify(term, syscall.SIGINT, syscall.SIGTERM)
		cancel := make(chan struct{})
		g.Add(func() error {
			for {
				select {
				case <-term:
					logger.Log("msg", "caught interrupt; gracefully cleaning up; see you next time!")
					return nil
				case <-cancel:
					return nil
				}
			}
		}, func(error) {
			close(cancel)
		})
	}

	pluginPath := viper.GetString("plugin-directory")
	for i := range deviceSpecs {
		d := deviceSpecs[i]

		enableUSBDiscovery := false
		for _, g := range d.Groups {
			if len(g.USBSpecs) > 0 {
				enableUSBDiscovery = true
				break
			}
		}

		ctx, cancel := context.WithCancel(context.Background())
		gp := deviceplugin.NewGenericPlugin(d, pluginPath, log.With(logger, "resource", d.Name), prometheus.WrapRegistererWith(prometheus.Labels{"resource": d.Name}, r), enableUSBDiscovery)
		// Start the generic device plugin server.
		g.Add(func() error {
			logger.Log("msg", fmt.Sprintf("Starting the generic-device-plugin for %q.", d.Name))
			return gp.Run(ctx)
		}, func(error) {
			cancel()
		})
	}

	return g.Run()
}

func main() {
	if err := Main(); err != nil {
		fmt.Fprintf(os.Stderr, "Execution failed: %v\n", err)
		os.Exit(1)
	}
}
