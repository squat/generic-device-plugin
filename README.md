# Kubernetes Generic Device Plugin

The generic-device-plugin enables allocating generic Linux devices, such as serial devices, the FUSE device, or video cameras, to Kubernetes Pods.
This allows devices that don't require special drivers to be advertised to the cluster and scheduled, enabling various use-cases, e.g.:
* accessing video and sound devices;
* running IoT applications, which often require access to hardware devices; and
* mounting FUSE filesysems without `privileged`.

[![Build Status](https://github.com/squat/generic-device-plugin/workflows/CI/badge.svg)](https://github.com/squat/generic-device-plugin/actions?query=workflow%3ACI)
[![Go Report Card](https://goreportcard.com/badge/github.com/squat/generic-device-plugin)](https://goreportcard.com/report/github.com/squat/generic-device-plugin)

## Overview

The generic-device-plugin can be configured to discover and allocate any desired device using the `--device` flag.
For example, to advertise all video devices to the cluster, the following flag could be given:
```
--device='{"name": "video", "groups": [{"paths": [{"path": "/dev/video0"}]}]}'
```

Now, Pods that require a video capture device, such as an object detection service, could request to be allocated one using the Kubernetes Pod `resources` field:
```yaml
resources:
  limits:
    squat.ai/video: 1
```

The `--device` flag can be provided multiple times to allow the plugin to discover and allocate different types of resources.

## Getting Started

To install the generic-device-plugin, choose what devices should be discovered and deploy the included DaemonSet:

```shell
kubectl apply -f https://raw.githubusercontent.com/squat/generic-device-plugin/main/manifests/generic-device-plugin.yaml
```

*Note*: the example manifest included in this repository discovers serial devices, the `/dev/video0` device, the `/dev/fuse` device, sound devices, and sound capture devices.

Now, deploy a workload that requests one of the newly discovered resources.
For example, the following script could be used to run a Pod that creates an MJPEG stream from a video device on the node:

```shell
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: mjpeg
  labels:
    app.kubernetes.io/name: mjpeg
spec:
  containers:
  - name: kceu
    image: squat/kubeconeu2019
    command:
    - /cam2ip
    args:
    - --bind-addr=:8080
    ports:
    - containerPort: 8080
      name: http
    resources:
      limits:
        squat.ai/video: 1
EOF
```

This application could then be accessed by port-forwarding to the Pod:

```shell
kubectl port-forward mjpeg http
```

Now, the MJPEG stream could be opened by pointing a browser to [http://localhost:8080/mjpeg](http://localhost:8080/mjpeg).


## Usage

[embedmd]:# (tmp/help.txt)
```txt
Usage of bin/amd64/generic-device-plugin:
      --config string             Path to the config file.
      --device stringArray        The devices to expose. This flag can be repeated to specify multiple device types.
                                  Multiple paths can be given for each type. Paths can be globs.
                                  Should be provided in the form:
                                  {"name": "<name>", "groups": [(device definitions)], "count": <count>}]}
                                  The device definition can be either a path to a device file or a USB device. You cannot define both in the same group.
                                  For device files, use something like: {"paths": [{"path": "<path-1>", "mountPath": "<mount-path-1>"},{"path": "<path-2>", "mountPath": "<mount-path-2>"}]}
                                  For USB devices, use something like: {"usb": [{"vendor": "1209", "product": "000F"}]}
                                  For example, to expose serial devices with different names: {"name": "serial", "groups": [{"paths": [{"path": "/dev/ttyUSB*"}]}, {"paths": [{"path": "/dev/ttyACM*"}]}]}
                                  The device flag can specify lists of devices that should be grouped and mounted into a container together as one single meta-device.
                                  For example, to allocate and mount an audio capture device: {"name": "capture", "groups": [{"paths": [{"path": "/dev/snd/pcmC0D0c"}, {"path": "/dev/snd/controlC0"}]}]}
                                  For example, to expose a CH340 serial converter: {"name": "ch340", "groups": [{"usb": [{"vendor": "1a86", "product": "7523"}]}]}
                                  A "count" can be specified to allow a discovered device group to be scheduled multiple times.
                                  For example, to permit allocation of the FUSE device 10 times: {"name": "fuse", "groups": [{"count": 10, "paths": [{"path": "/dev/fuse"}]}]}
                                  Note: if omitted, "count" is assumed to be 1
      --domain string             The domain to use when when declaring devices. (default "squat.ai")
      --listen string             The address at which to listen for health and metrics. (default ":8080")
      --log-level string          Log level to use. Possible values: all, debug, info, warn, error, none (default "info")
      --plugin-directory string   The directory in which to create plugin sockets. (default "/var/lib/kubelet/device-plugins/")
      --version                   Print version and exit
```
