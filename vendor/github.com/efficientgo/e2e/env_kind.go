// Copyright (c) The EfficientGo Authors.
// Licensed under the Apache License 2.0.

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/efficientgo/core/backoff"
	"github.com/efficientgo/core/errors"
)

var (
	kindNamePattern = regexp.MustCompile(`^[a-z0-9.-]+$`)

	_ Environment = &KindEnvironment{}
)

// KindEnvironment defines single node kind cluster that allows running services.
type KindEnvironment struct {
	dir         string
	logger      Logger
	clusterName string

	nodeIP  net.IP
	volumes []string
	verbose bool

	mutex sync.Mutex
	// Access to the following fields must be guarded
	// by a mutex.
	registered map[string]struct{}
	listeners  []EnvironmentListener
	started    []Runnable
	closers    []func()
	closed     bool
}

func validateKindName(name string) error {
	if len(name) == 0 {
		return errors.New("name can't be empty")
	}
	if !kindNamePattern.MatchString(name) {
		return errors.Newf("name can have only %v characters due to kind cluster name constraints, got: %v", kindNamePattern.String(), name)

	}
	return nil
}

func unwrapQuotes(b []byte) []byte {
	return []byte(strings.Trim(string(b), "'"))
}

var kindConfig = template.Must(template.New("config").Parse(`kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  {{- with .}}
  extraMounts:
  {{- range .}}
  - hostPath: {{.}}
    containerPath: {{.}}
  {{- end}}
  {{- end}}
`))

// NewKindEnvironment creates a new, isolated kind environment.
func NewKindEnvironment(opts ...EnvironmentOption) (_ *KindEnvironment, err error) {
	e := environmentOptions{}
	for _, o := range opts {
		o(&e)
	}
	if e.name == "" {
		e.name, err = generateName()
		if err != nil {
			return nil, err
		}
		e.name = strings.ToLower(e.name)
	}
	if err := validateKindName(e.name); err != nil {
		return nil, err
	}

	if e.logger == nil {
		e.logger = NewLogger(os.Stdout)
	}

	k := &KindEnvironment{
		logger:      e.logger,
		clusterName: e.name,
		verbose:     e.verbose,
		registered:  map[string]struct{}{},
		volumes:     e.volumes,
	}

	// Force a shutdown in order to cleanup from a spurious situation in case
	// the previous tests run didn't cleanup correctly.
	k.close()

	dir, err := getTmpDirectory()
	if err != nil {
		return nil, err
	}
	k.dir = dir
	var buf bytes.Buffer
	if err := kindConfig.Execute(&buf, append([]string{k.dir}, k.volumes...)); err != nil {
		k.Close()
		return nil, errors.Wrap(err, "generate cluster kind configuration")
	}
	kindConfigPath := filepath.Join(k.dir, "kind.yaml")
	if err := os.WriteFile(kindConfigPath, buf.Bytes(), 0644); err != nil {
		k.Close()
		return nil, errors.Wrap(err, "write cluster kind configuration")
	}

	// Setup the kind cluster.
	if out, err := k.exec("kind", "create", "cluster", "--kubeconfig", k.kubeconfig(), "--config", kindConfigPath, "--name", k.clusterName).CombinedOutput(); err != nil {
		e.logger.Log(string(out))
		k.Close()
		return nil, errors.Wrapf(err, "create kind cluster %q", k.clusterName)
	}

	out, err := k.exec("kubectl", "--kubeconfig", k.kubeconfig(), "get", "nodes", fmt.Sprintf("%s-control-plane", k.clusterName), "--output", `jsonpath='{.status.addresses}'`).CombinedOutput()
	if err != nil {
		e.logger.Log(string(out))
		k.Close()
		return nil, errors.Wrapf(err, "get details of kind cluster node '%s-control-plane'", k.clusterName)
	}

	type address struct {
		Address string
		Type    string
	}
	var addresses []address
	out = unwrapQuotes(out)
	if err := json.Unmarshal(out, &addresses); err != nil {
		e.logger.Log("my string without quotes")
		e.logger.Log(len(out))
		e.logger.Log(string(out))
		k.Close()
		return nil, errors.Wrap(err, "unmarshal kubectl output to get node IP")
	}

	var internalIPs []address
	for _, a := range addresses {
		if a.Type == "InternalIP" {
			internalIPs = append(internalIPs, a)
		}
	}
	if len(internalIPs) != 1 {
		k.Close()
		return nil, errors.Newf("unexpected output of kubectl get node; expected exactly one internal IP, got %d", len(internalIPs))
	}
	k.nodeIP = net.ParseIP(internalIPs[0].Address)

	return k, e.logger.Log("msg", "started kind environment", "name", k.clusterName)
}

func (e *KindEnvironment) kubeconfig() string { return filepath.Join(e.dir, "kubeconfig") }

func (e *KindEnvironment) HostAddr() string { return e.nodeIP.String() }
func (e *KindEnvironment) Name() string     { return e.clusterName }

func (e *KindEnvironment) AddCloser(f func()) {
	defer e.mutex.Unlock()
	e.mutex.Lock()
	e.closers = append(e.closers, f)
}

// Runnable returns a new RunnableBuilder for the KindEnvironment.
// Note: runnables are modeled as Kubernetes Deployments with a single replica.
// If a runnable of a different Kubernetes kind is needed, then it must be
// manually deployed using the kubeconfig in the environment's shared directory.
func (e *KindEnvironment) Runnable(name string) RunnableBuilder {
	defer e.mutex.Unlock()
	e.mutex.Lock()
	if e.closed {
		return errorer{name: name, err: errors.New("environment close was already invoked")}
	}

	if e.isRegistered(name) {
		return errorer{name: name, err: errors.Newf("there is already one runnable created with the same name %q", name)}
	}

	r := &kindRunnable{
		env:        e,
		name:       name,
		logger:     e.logger,
		ports:      map[string]int{},
		hostPorts:  map[string]int{},
		extensions: map[any]any{},
	}
	if err := os.MkdirAll(r.Dir(), 0750); err != nil {
		return errorer{name: name, err: err}
	}
	e.register(name)
	return r
}

// AddListener registers the given listener to be notified on environment runnable changes.
func (e *KindEnvironment) AddListener(listener EnvironmentListener) {
	defer e.mutex.Unlock()
	e.mutex.Lock()
	e.listeners = append(e.listeners, listener)
}

func (e *KindEnvironment) isRegistered(name string) bool {
	_, ok := e.registered[name]
	return ok
}

func (e *KindEnvironment) register(name string) {
	e.registered[name] = struct{}{}
}

func (e *KindEnvironment) registerStarted(r Runnable) error {
	e.started = append(e.started, r)

	for _, l := range e.listeners {
		if err := l.OnRunnableChange(e.started); err != nil {
			return err
		}
	}
	return nil
}

func (e *KindEnvironment) registerStopped(name string) error {
	for i, r := range e.started {
		if r.Name() == name {
			e.started = append(e.started[:i], e.started[i+1:]...)
			for _, l := range e.listeners {
				if err := l.OnRunnableChange(e.started); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return nil
}

func (e *KindEnvironment) SharedDir() string {
	return e.dir
}

func (e *KindEnvironment) buildManifest(name string, ports map[string]int, opts StartOptions) (io.Reader, error) {
	values := kindManifestValues{
		Name:         name,
		Image:        opts.Image,
		Command:      opts.Command.Cmd,
		Args:         opts.Command.Args,
		Ports:        ports,
		Envs:         opts.EnvVars,
		Bytes:        opts.LimitMemoryBytes,
		CPUs:         opts.LimitCPUs,
		Privileged:   opts.Privileged,
		Capabilities: opts.Capabilities,
		Volumes: map[string]string{
			// Mount the working directory into the container. It's shared across all containers to allow easier scenarios.
			"working-directory": e.dir,
		},
		User:   opts.User,
		UserNs: opts.UserNs,
	}
	for i, v := range e.volumes {
		values.Volumes[fmt.Sprintf("volume%d", i)] = v
	}
	for i, v := range opts.Volumes {
		values.Volumes[fmt.Sprintf("volume%d", i+len(e.volumes))] = v
	}
	var buf bytes.Buffer
	if err := kindManifest.Execute(&buf, values); err != nil {
		return nil, err
	}
	return &buf, nil
}

var kindManifest = template.Must(template.New("manifest").Parse(`apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    app.kubernetes.io/name: "{{.Name}}"
  name: "{{.Name}}"
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: "{{.Name}}"
  template:
    metadata:
      labels:
        app.kubernetes.io/name: "{{.Name}}"
    spec:
      containers:
      - name: "{{.Name}}"
        image: "{{.Image}}"
        {{- with .Command}}
        command:
        - "{{.}}"
        {{- end}}
        {{- with .Args}}
        args:
        {{- range .}}
        - "{{.}}"
        {{- end}}
        {{- end}}
        {{- with .Ports}}
        ports:
        {{- range $k, $v := .}}
        - name: "{{$k}}"
          containerPort: {{$v}}
        {{- end}}
        {{- end}}
        {{- with .Envs}}
        env:
        {{- range $k, $v := .}}
        - name: "{{$k}}"
          value: "{{$v}}"
        {{- end}}
        {{- end}}
        {{- if or .Bytes .CPUs}}
        resources:
          limits:
            {{- with .Bytes}}
            memory: {{.}}
            {{- end}}
            {{- with .CPUs}}
            cpu: {{.}}
            {{- end}}
          requests:
            {{- with .Bytes}}
            memory: {{.}}
            {{- end}}
            {{- with .CPUs}}
            cpu: {{.}}
            {{- end}}
        {{- end}}
        {{- if or .Privileged .Capabilities .User}}
        securityContext:
          {{- with .User}}
          runAsUser: {{.}}
          {{- end}}
          {{- if .Privileged}}
          privileged: true
          {{- end}}
          {{- with .Capabilities}}
          capabilities:
            add:
            {{- range .}}
            - {{.}}
            {{- end}}
          {{- end}}
        {{- end}}
        {{- with .Volumes}}
        volumeMounts:
        {{- range $k, $v := .}}
        - name: "{{$k}}"
          mountPath: {{$v}}
        {{- end}}
        {{- end}}
      {{- with .Volumes}}
      volumes:
      {{- range $k, $v := .}}
      - name: "{{$k}}"
        hostPath:
          path: {{$v}}
      {{- end}}
      {{- end}}
{{- if .Ports}}
---
apiVersion: v1
kind: Service
metadata:
  labels:
    app.kubernetes.io/name: "{{.Name}}"
  name: "{{.Name}}"
spec:
  type: NodePort
  selector:
    app.kubernetes.io/name: "{{.Name}}"
  {{- with .Ports}}
  ports:
  {{- range $k, $v := .}}
  - name: "{{$k}}"
    port: {{$v}}
  {{- end}}
  {{- end}}
{{- end}}
`))

type kindManifestValues struct {
	Name         string
	Image        string
	Command      string
	Args         []string
	Ports        map[string]int
	Envs         map[string]string
	Bytes        uint
	CPUs         float64
	Privileged   bool
	Capabilities []RunnableCapabilities
	Volumes      map[string]string
	User         string
	UserNs       string
}

func (e *KindEnvironment) Close() {
	defer e.mutex.Unlock()
	e.mutex.Lock()
	for _, c := range e.closers {
		c()
	}
	e.close()
	e.closed = true
}

func (e *KindEnvironment) exec(cmd string, args ...string) *exec.Cmd {
	return e.execContext(context.Background(), cmd, args...)
}

func (e *KindEnvironment) execContext(ctx context.Context, cmd string, args ...string) *exec.Cmd {
	c := NewCommand(cmd, args...)
	if e.verbose {
		e.logger.Log("kindEnv:", c.toString())
	}
	return c.exec(ctx)
}

func (e *KindEnvironment) close() {
	if e == nil || e.closed {
		return
	}

	// Teardown the kind cluter.
	// Kind is idempotent and doesn't care if the cluster doesn't exist, it won't throw an error.
	if out, err := e.exec("kind", "delete", "cluster", "--name", e.clusterName).CombinedOutput(); err != nil {
		e.logger.Log(string(out))
		e.logger.Log("Unable to delete kind cluster", e.clusterName, ":", err.Error())
	}

	if e.dir != "" {
		if err := e.exec("chmod", "-R", "777", e.dir).Run(); err != nil {
			e.logger.Log("Error while chmod sharedDir", e.dir, "err:", err)
		}
		if err := os.RemoveAll(e.dir); err != nil {
			e.logger.Log("Error while removing sharedDir", e.dir, "err:", err)
		}
	}
}

type kindRunnable struct {
	env    *KindEnvironment
	name   string
	logger Logger

	mutex sync.Mutex
	// Access to the following fields must be guarded
	// by a mutex.
	ports            map[string]int
	opts             StartOptions
	running          bool
	hostPorts        map[string]int
	extensions       map[any]any
	waitBackoffReady *backoff.Backoff
}

func (r *kindRunnable) Name() string {
	return r.name
}

func (r *kindRunnable) BuildErr() error {
	return nil
}

func (r *kindRunnable) Dir() string {
	return filepath.Join(r.env.SharedDir(), "data", r.Name())
}

func (r *kindRunnable) InternalDir() string {
	return r.Dir()
}

func (r *kindRunnable) Init(opts StartOptions) Runnable {
	defer r.mutex.Unlock()
	r.mutex.Lock()

	if opts.WaitReadyBackoff == nil {
		opts.WaitReadyBackoff = &backoff.Config{
			Min:        300 * time.Millisecond,
			Max:        600 * time.Millisecond,
			MaxRetries: 50, // Sometimes the CI is slow ¯\_(ツ)_/¯.
		}
	}

	r.opts = opts
	r.waitBackoffReady = backoff.New(context.Background(), *opts.WaitReadyBackoff)
	return r
}

func (r *kindRunnable) WithPorts(ports map[string]int) RunnableBuilder {
	defer r.mutex.Unlock()
	r.mutex.Lock()

	r.ports = ports
	return r
}

func (r *kindRunnable) SetMetadata(key, value any) {
	defer r.mutex.Unlock()
	r.mutex.Lock()

	r.extensions[key] = value
}

func (r *kindRunnable) GetMetadata(key any) (any, bool) {
	defer r.mutex.Unlock()
	r.mutex.Lock()

	v, ok := r.extensions[key]
	return v, ok
}

func (r *kindRunnable) Future() FutureRunnable {
	return r
}

func (r *kindRunnable) IsRunning() bool {
	defer r.mutex.Unlock()
	r.mutex.Lock()

	return r.running
}

// Start starts the runnable.
func (r *kindRunnable) Start() (err error) {
	if r.IsRunning() {
		return errors.Newf("%q is running; stop or kill it first to restart", r.Name())
	}

	r.logger.Log("Starting", r.Name())

	// In case of any error, if the container was already created, we
	// have to cleanup removing it.
	defer func() {
		if err != nil {
			if out, err := r.env.exec("kubernetes", "delete", "deployment", r.Name(), "--ignore-not-found", "--grace-period", "0", "--force").CombinedOutput(); err != nil {
				r.logger.Log(string(out))
				return
			}
			if out, err := r.env.exec("kubernetes", "delete", "service", r.Name(), "--ignore-not-found", "--grace-period", "0", "--force").CombinedOutput(); err != nil {
				r.logger.Log(string(out))
				return
			}
		}
	}()

	// Make sure the image is available locally; if not wait for it to download.
	if err := r.prePullImage(context.TODO()); err != nil {
		return err
	}

	defer r.mutex.Unlock()
	r.mutex.Lock()
	manifest, err := r.env.buildManifest(r.name, r.ports, r.opts)
	if err != nil {
		return errors.Wrap(err, "building manifest")
	}
	cmd := r.env.exec("kubectl", "--kubeconfig", r.env.kubeconfig(), "apply", "--filename", "-")
	l := &LinePrefixLogger{prefix: r.Name() + ": ", logger: r.logger}
	cmd.Stdout = l
	cmd.Stderr = l
	cmd.Stdin = manifest
	if err := cmd.Start(); err != nil {
		return err
	}
	r.running = true

	// Wait until the container has been started.
	if err := r.waitForRunning(); err != nil {
		return err
	}

	if err := r.env.registerStarted(r); err != nil {
		return err
	}

	if len(r.ports) > 0 {
		// Get the dynamic local ports mapped to the container.
		out, err := r.env.exec("kubectl", "--kubeconfig", r.env.kubeconfig(), "get", "service", r.Name(), "--output", `jsonpath='{.spec.ports}'`).CombinedOutput()
		if err != nil {
			return errors.Wrapf(err, "unable to get mapping for ports for service %q; output: %q", r.Name(), out)
		}
		var ports []struct {
			Name     string
			NodePort int
		}
		out = unwrapQuotes(out)
		if err := json.Unmarshal(out, &ports); err != nil {
			return errors.Wrap(err, "unmarshal kubectl output to get ports")
		}
		if len(ports) != len(r.ports) {
			return errors.Newf("found inconsistent ports: the running service %q has a different number of ports than declared", r.Name())
		}
		for _, port := range ports {
			if _, ok := r.ports[port.Name]; !ok {
				return errors.Newf("found inconsistent ports: port %q is not declared in service %q", port.Name, r.Name())
			}
			r.hostPorts[port.Name] = port.NodePort
		}

		r.logger.Log("Ports for container", r.Name(), ">> Local ports:", r.ports, "Ports available from host:", r.hostPorts)
	}

	return nil
}

func (r *kindRunnable) Stop() error {
	if !r.IsRunning() {
		return nil
	}

	r.logger.Log("Stopping", r.Name())
	if out, err := r.env.exec("kubernetes", "delete", "deployment", r.Name(), "--ignore-not-found", "--grace-period", "30").CombinedOutput(); err != nil {
		r.logger.Log(string(out))
		return err
	}
	if out, err := r.env.exec("kubernetes", "delete", "service", r.Name(), "--ignore-not-found", "--grace-period", "30").CombinedOutput(); err != nil {
		r.logger.Log(string(out))
		return err
	}
	defer r.mutex.Unlock()
	r.mutex.Lock()
	r.running = false
	return r.env.registerStopped(r.Name())
}

func (r *kindRunnable) Kill() error {
	if !r.IsRunning() {
		return nil
	}

	r.logger.Log("Killing", r.Name())
	if out, err := r.env.exec("kubernetes", "delete", "deployment", r.Name(), "--ignore-not-found", "--grace-period", "0", "--force").CombinedOutput(); err != nil {
		r.logger.Log(string(out))
		return err
	}
	if out, err := r.env.exec("kubernetes", "delete", "service", r.Name(), "--ignore-not-found", "--grace-period", "0", "--force").CombinedOutput(); err != nil {
		r.logger.Log(string(out))
		return err
	}

	defer r.mutex.Unlock()
	r.mutex.Lock()
	r.running = false
	return r.env.registerStopped(r.Name())
}

// Endpoint returns the external service endpoint (host:port) for a given port name.
// External means that it will be accessible from the host.
// If the service is not running, this method returns the incorrect `stopped` endpoint.
func (r *kindRunnable) Endpoint(portName string) string {
	if !r.IsRunning() {
		return "stopped"
	}

	defer r.mutex.Unlock()
	r.mutex.Lock()

	// Map the container port to the local port.
	localPort, ok := r.hostPorts[portName]
	if !ok {
		return ""
	}

	return fmt.Sprintf("%s:%d", r.env.nodeIP, localPort)
}

// InternalEndpoint returns the internal service endpoint (host:port) for a given internal port.
// Internal means that it will be accessible only from containers in the environment that this
// service is running in. Use `Endpoint` for host access.
func (r *kindRunnable) InternalEndpoint(portName string) string {
	defer r.mutex.Unlock()
	r.mutex.Lock()

	// Map the port name to the container port.
	port, ok := r.ports[portName]
	if !ok {
		return ""
	}

	return fmt.Sprintf("%s:%d", r.Name(), port)
}

func (r *kindRunnable) Ready() error {
	if !r.IsRunning() {
		return errors.Newf("service %s is stopped", r.Name())
	}

	r.mutex.Lock()
	readiness := r.opts.Readiness
	r.mutex.Unlock()
	// Ensure the service has a readiness probe configured.
	if readiness == nil {
		return nil
	}

	return readiness.Ready(r)
}

func (r *kindRunnable) waitForRunning() (err error) {
	if !r.running {
		return errors.Newf("service %s is stopped", r.Name())
	}

	var out []byte
	for r.waitBackoffReady.Reset(); r.waitBackoffReady.Ongoing(); {
		// Enforce a timeout on the command execution because we've seen some flaky tests
		// stuck here.

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err = r.env.execContext(
			ctx,
			"kubectl",
			"--kubeconfig",
			r.env.kubeconfig(),
			"wait",
			"pod",
			"--for",
			"condition=Ready",
			"--selector",
			fmt.Sprintf("app.kubernetes.io/name=%s", r.Name()),
			"--timeout",
			"5s",
		).CombinedOutput()
		if err != nil {
			r.waitBackoffReady.Wait()
			continue
		}

		return nil
	}

	if len(out) > 0 {
		r.logger.Log(string(out))
	}
	return errors.Wrapf(err, "pod %q failed to start", r.Name())
}

// We want to pre-pull all images using Docker and then load them into the cluster.
// This ensures that the cluster will always have access to any locally built images.
func (r *kindRunnable) prePullImage(ctx context.Context) (err error) {
	if r.running {
		return errors.Newf("service %q is running; expected stopped", r.Name())
	}

	if _, err = r.env.execContext(ctx, "docker", "image", "inspect", r.opts.Image).CombinedOutput(); err == nil {
		return nil
	}

	// Assuming Error: No such image: <image>.
	cmd := r.env.execContext(ctx, "docker", "pull", r.opts.Image)
	l := &LinePrefixLogger{prefix: r.Name() + ": ", logger: r.logger}
	cmd.Stdout = l
	cmd.Stderr = l
	if err = cmd.Run(); err != nil {
		return errors.Wrapf(err, "docker image %q failed to download", r.opts.Image)
	}

	if err := r.env.execContext(ctx, "docker", "image", "inspect", r.opts.Image).Run(); err == nil {
		return errors.Wrapf(err, "load image %q into cluster", r.opts.Image)

	}

	return nil
}

func (r *kindRunnable) WaitReady() (err error) {
	if !r.IsRunning() {
		return errors.Newf("service %s is stopped", r.Name())
	}

	for r.waitBackoffReady.Reset(); r.waitBackoffReady.Ongoing(); {
		err = r.Ready()
		if err == nil {
			return nil
		}

		r.waitBackoffReady.Wait()
	}
	return errors.Wrapf(err, "service %q is not ready", r.Name())
}

// Exec runs the provided command in the container specified by this
// service.
func (r *kindRunnable) Exec(command Command, opts ...ExecOption) error {
	if !r.IsRunning() {
		return errors.Newf("service %q is stopped", r.Name())
	}

	l := &LinePrefixLogger{prefix: r.Name() + "-exec: ", logger: r.logger}
	o := ExecOptions{Stdout: l, Stderr: l}
	for _, opt := range opts {
		opt(&o)
	}

	args := []string{"kubectl", "--kubeconfig", r.env.kubeconfig(), "exec", "deployment/" + r.name, "--"}
	args = append(args, command.Cmd)
	args = append(args, command.Args...)
	cmd := r.env.exec(args[0], args[1:]...)
	cmd.Stdout = o.Stdout
	cmd.Stderr = o.Stderr
	return cmd.Run()
}
