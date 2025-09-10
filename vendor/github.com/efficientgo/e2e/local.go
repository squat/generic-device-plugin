// Copyright (c) The EfficientGo Authors.
// Licensed under the Apache License 2.0.

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"github.com/efficientgo/core/errors"
)

// Containerize inspects startFn and builds Go shim with local process endpoint that imports given `startFn` function.
// Binary is then put in adhoc container and returned as runnable ready to be started.
func Containerize(e Environment, name string, startFn func(context.Context) error) (Runnable, error) {
	de, ok := e.(*DockerEnvironment)
	if !ok {
		return nil, errors.New("not implemented")
	}

	// Not portable, but good enough for local unit tests.
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	strs := strings.Split(runtime.FuncForPC(reflect.ValueOf(startFn).Pointer()).Name(), ".")
	funcName := strs[len(strs)-1]
	pkg := strings.Join(strs[:len(strs)-1], ".")

	modulePath := pkg
	absModulePath := wd
	for len(absModulePath) > 0 {
		_, err := os.Stat(filepath.Join(absModulePath, "go.mod"))
		if os.IsNotExist(err) {
			absModulePath = filepath.Dir(absModulePath)
			modulePath = filepath.Dir(modulePath)
			continue
		}
		if err == nil {
			break
		}
		return nil, err
	}

	if len(absModulePath) == 0 {
		return nil, errors.Newf("not a Go module %v", wd)
	}

	f := e.Runnable(name).WithPorts(map[string]int{"http": 80}).Future()
	dir := filepath.Join(f.Dir(), "shim")

	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return nil, err
	}

	// TODO(saswatamcode): Maybe we can do away with goModTmpl, and just run go mod init shim, go mod edit -replace %v=%v and go mod tidy here.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(fmt.Sprintf(goModTmpl, modulePath, modulePath, absModulePath)), os.ModePerm); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(fmt.Sprintf(mainFileTmpl, pkg, funcName)), os.ModePerm); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerFile), os.ModePerm); err != nil {
		return nil, err
	}

	cmd := de.exec("go", "mod", "tidy")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, errors.Wrap(err, string(out))
	}

	cmd = de.exec("go", "build", "-o", "exe", "main.go")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, errors.Wrap(err, string(out))
	}

	imageTag := fmt.Sprintf("e2e-local-%v:dynamic", name)
	cmd = de.exec("docker", "build", "-t", imageTag, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, errors.Wrap(err, string(out))
	}
	return f.Init(StartOptions{Image: imageTag}), nil
}

const (
	dockerFile = `FROM ubuntu:21.10

ADD ./exe /bin/exe

ENTRYPOINT [ "/bin/exe" ]`

	mainFileTmpl = `package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"sync"

	local "%v"

	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func exe() error {
	// Expose metrics from the current process.
	metrics := prometheus.NewRegistry()
	metrics.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := http.NewServeMux()
	h := promhttp.HandlerFor(metrics, promhttp.HandlerOpts{})
	o := sync.Once{}
	scraped := make(chan struct{})
	m.Handle("/metrics", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		o.Do(func() { close(scraped) })
		h.ServeHTTP(w, req)
	}))

	list, err := net.Listen("tcp", "0.0.0.0:80")
	if err != nil {
		return err
	}
	s := http.Server{Handler: m}

	g := run.Group{}
	{
		ctx, cancel := context.WithCancel(context.Background())
		g.Add(func() error {
			return local.%v(ctx)
		}, func(_ error) {
			cancel()
		})
	}

	g.Add(func() error {
		return s.Serve(list)
	}, func(_ error) {
		_ = s.Close()
	})
	g.Add(run.SignalHandler(context.Background(), os.Interrupt))
	return g.Run()
}

func main() {
	if err := exe(); err != nil {
		log.Fatal(err)
	}
}
`

	goModTmpl = `module shim

go 1.17

require (
	github.com/oklog/run v1.1.0
	github.com/prometheus/client_golang v1.12.1
	%v v1.0.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.1.2 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.1 // indirect
	github.com/prometheus/client_model v0.2.0 // indirect
	github.com/prometheus/common v0.32.1 // indirect
	github.com/prometheus/procfs v0.7.3 // indirect
	golang.org/x/sys v0.0.0-20220114195835-da31bd327af9 // indirect
	google.golang.org/protobuf v1.26.0 // indirect
)

replace (
	%v => %v
)`
)
