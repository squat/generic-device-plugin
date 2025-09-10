// Copyright (c) The EfficientGo Authors.
// Licensed under the Apache License 2.0.

package e2e

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/efficientgo/core/backoff"
	"github.com/efficientgo/core/errcapture"
	"github.com/efficientgo/core/errors"
)

// EnvironmentOption defined the signature of a function used to manipulate options.
type EnvironmentOption func(*environmentOptions)

type environmentOptions struct {
	logger  Logger
	verbose bool
	name    string

	volumes []string
}

// WithLogger tells environment to use custom logger to default one (stdout).
func WithLogger(logger Logger) EnvironmentOption {
	return func(o *environmentOptions) {
		o.logger = logger
	}
}

// WithVerbose tells environment to be verbose i.e print all commands it executes.
func WithVerbose() EnvironmentOption {
	return func(o *environmentOptions) {
		o.verbose = true
	}
}

// WithName injects custom name of environment which will be used as a network name.
// If the name is not unique across different e2e environments ran at the same moment this will race them.
// In the same time if name is unique across every environment run for the same test it won't be able
// to easily clean up (so be reused) on the next run if the network was not cleaned properly.
//
// By default, it creates `e2e_<hash based on function name>` environment name.
//
// NOTE: Some restrictions apply. See https://stackoverflow.com/a/53478768.
func WithName(name string) EnvironmentOption {
	return func(o *environmentOptions) {
		o.name = name
	}
}

func WithVolumes(volumes ...string) EnvironmentOption {
	return func(o *environmentOptions) {
		o.volumes = volumes
	}
}

// Environment defines how to run Runnable in isolated area e.g via docker in isolated docker network.
type Environment interface {
	// Name returns environment name.
	Name() string
	// SharedDir returns host directory that will be shared with all runnables.
	SharedDir() string
	// HostAddr returns host address that is available from runnables.
	HostAddr() string
	// Runnable returns runnable builder which can build runnables that can be started and stopped within this environment.
	Runnable(name string) RunnableBuilder
	// AddListener registers given listener to be notified on environment runnable changes.
	AddListener(listener EnvironmentListener)
	// AddCloser registers function to be invoked on close, before all containers are sent kill signal.
	AddCloser(func())
	// Close shutdowns isolated environment and cleans its resources.
	Close()
}

type EnvironmentListener interface {
	OnRunnableChange(started []Runnable) error
}

// StartOptions represents starting option of runnable in the environment.
type StartOptions struct {
	Image     string
	EnvVars   map[string]string
	User      string
	Command   Command
	Readiness ReadinessProbe
	// WaitReadyBackofff represents backoff used for WaitReady.
	WaitReadyBackoff *backoff.Config
	Volumes          []string
	UserNs           string
	Privileged       bool
	Capabilities     []RunnableCapabilities

	LimitMemoryBytes uint
	LimitCPUs        float64
}

type RunnableCapabilities string

const (
	RunnableCapabilitiesSysAdmin RunnableCapabilities = "SYS_ADMIN"
)

// Linkable is the entity that one can use to link runnable to other runnables before started.
type Linkable interface {
	// Name returns unique name for the Runnable instance.
	Name() string

	// Dir returns the working directory path that is shared with the host and this runnable. The paths are exactly
	// the same for runnable and host to enable symbolic links (as long as those don't link to paths outside of Dir or not
	// otherwise shared with runnable).
	Dir() string

	// Deprecated. Use Dir instead. For compatibility Dir() is returned.
	InternalDir() string

	// InternalEndpoint returns internal runnable endpoint (host:port) for given internal port.
	// Internal means that it will be accessible only from runnable context.
	InternalEndpoint(portName string) string
}

type FutureRunnable interface {
	Linkable

	// Init transforms future into runnable.
	Init(opts StartOptions) Runnable
}

// RunnableBuilder represents options that can be build into runnable and if
// you want Future or Initiated Runnable from it.
type RunnableBuilder interface {
	// WithPorts adds ports to runnable, allowing caller to
	// use `InternalEndpoint` and `Endpoint` methods by referencing port by name.
	WithPorts(map[string]int) RunnableBuilder
	// Future returns future runnable
	Future() FutureRunnable
	// Init returns runnable.
	Init(opts StartOptions) Runnable
}

type runnable interface {
	// BuildErr returns error if runnable failed to build. If error happened during build all methods like
	// Start, WaitReady, Kill and Stop will return this error. Rest of the methods will yield empty results, so if you
	// want to use those before any of the Start, WaitReady, Kill or Stop, you can use BuildErr to check for error explicitly.
	BuildErr() error

	// IsRunning returns if runnable was started.
	IsRunning() bool

	// Start tells Runnable to start.
	Start() error

	// WaitReady waits until the Runnable is ready. It should return error if runnable is stopped in mean time or
	// it was stopped before.
	WaitReady() error

	// Kill tells Runnable to get killed immediately.
	// It should be ok to Stop and Kill more than once, with next invokes being noop.
	Kill() error

	// Stop tells Runnable to get gracefully stopped.
	// It should be ok to Stop and Kill more than once, with next invokes being noop.
	Stop() error

	// Exec runs the provided command inside the same process context (e.g. in the running docker container).
	// It returns error response from attempting to run the command.
	// See ExecOptions for more options like returning output or attaching to e2e logging.
	Exec(Command, ...ExecOption) error

	// Endpoint returns external runnable endpoint (host:port) for given port name.
	// External means that it will be accessible only from host, but not from docker containers.
	//
	// If your service is not running, this method returns incorrect `stopped` endpoint.
	Endpoint(portName string) string

	// SetMetadata allows setting extra metadata describing runnable.
	SetMetadata(key, value any)
	// GetMetadata retrieves metadata by given key or return false if not found.
	GetMetadata(key any) (any, bool)
}

type ExecOption func(o *ExecOptions)

type ExecOptions struct {
	Stdout io.Writer
	Stderr io.Writer
}

// WithExecOptionStdout sets stdout writer to be used when exec is performed.
// By default, it is streaming to the env logger.
func WithExecOptionStdout(stdout io.Writer) ExecOption {
	return func(o *ExecOptions) {
		o.Stdout = stdout
	}
}

// WithExecOptionStderr sets stderr writer to be used when exec is performed.
// By default, it is streaming to the env logger.
func WithExecOptionStderr(stderr io.Writer) ExecOption {
	return func(o *ExecOptions) {
		o.Stderr = stderr
	}
}

// Runnable is the entity that environment returns to manage single instance.
type Runnable interface {
	runnable

	Linkable
}

func StartAndWaitReady(runnables ...Runnable) error {
	for _, r := range runnables {
		if err := r.Start(); err != nil {
			return err
		}
	}
	for _, r := range runnables {
		if err := r.WaitReady(); err != nil {
			return err
		}
	}
	return nil
}

type Command struct {
	Cmd                string
	Args               []string
	EntrypointDisabled bool
}

func NewCommand(cmd string, args ...string) Command {
	return Command{
		Cmd:  cmd,
		Args: args,
	}
}

func (c Command) toString() string {
	var a []string
	if c.Cmd != "" {
		a = append(a, c.Cmd)
	}
	return fmt.Sprint(append(a, c.Args...))
}

func (c Command) exec(ctx context.Context) *exec.Cmd {
	return exec.CommandContext(ctx, c.Cmd, c.Args...)
}

func NewCommandWithoutEntrypoint(cmd string, args ...string) Command {
	return Command{
		Cmd:                cmd,
		Args:               args,
		EntrypointDisabled: true,
	}
}

// NewCommandRunUntilStop is a command that allows to keep container running.
func NewCommandRunUntilStop() Command {
	return NewCommandWithoutEntrypoint("tail", "-f", "/dev/null")
}

type ReadinessProbe interface {
	Ready(runnable Runnable) (err error)
}

// HTTPReadinessProbe checks readiness by making HTTP or HTTPS call and checking for expected HTTP/HTTPS status code.
type HTTPReadinessProbe struct {
	portName                 string
	path                     string
	scheme                   string
	expectedStatusRangeStart int
	expectedStatusRangeEnd   int
	expectedContent          []string
}

func NewHTTPReadinessProbe(portName string, path string, expectedStatusRangeStart, expectedStatusRangeEnd int, expectedContent ...string) *HTTPReadinessProbe {
	return newHTTPReadinessProbe(portName, path, "HTTP",
		expectedStatusRangeStart, expectedStatusRangeEnd, expectedContent...)
}

func NewHTTPSReadinessProbe(portName, path string, expectedStatusRangeStart, expectedStatusRangeEnd int, expectedContent ...string) *HTTPReadinessProbe {
	return newHTTPReadinessProbe(portName, path, "HTTPS",
		expectedStatusRangeStart, expectedStatusRangeEnd, expectedContent...)
}

func newHTTPReadinessProbe(portName, path, scheme string, expectedStatusRangeStart, expectedStatusRangeEnd int, expectedContent ...string) *HTTPReadinessProbe {
	return &HTTPReadinessProbe{
		portName:                 portName,
		path:                     path,
		scheme:                   scheme,
		expectedStatusRangeStart: expectedStatusRangeStart,
		expectedStatusRangeEnd:   expectedStatusRangeEnd,
		expectedContent:          expectedContent,
	}
}

func (p *HTTPReadinessProbe) Ready(runnable Runnable) (err error) {
	endpoint := runnable.Endpoint(p.portName)
	if endpoint == "" {
		return errors.Newf("cannot get service endpoint for port %s", p.portName)
	}
	if endpoint == "stopped" {
		return errors.New("service has stopped")
	}

	httpClient := &http.Client{Timeout: 1 * time.Second}
	if p.scheme == "HTTPS" {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}

	res, err := httpClient.Get(p.scheme + "://" + endpoint + p.path)
	if err != nil {
		return err
	}
	defer errcapture.ExhaustClose(&err, res.Body, "response readiness")

	body, _ := io.ReadAll(res.Body)
	if res.StatusCode < p.expectedStatusRangeStart || res.StatusCode > p.expectedStatusRangeEnd {
		return errors.Newf("expected code in range: [%v, %v], got status code: %v and body: %v", p.expectedStatusRangeStart, p.expectedStatusRangeEnd, res.StatusCode, string(body))
	}

	for _, expected := range p.expectedContent {
		if !strings.Contains(string(body), expected) {
			return errors.Newf("expected body containing %s, got: %v", expected, string(body))
		}
	}
	return nil
}

// TCPReadinessProbe checks readiness by ensure a TCP connection can be established.
type TCPReadinessProbe struct {
	portName string
}

func NewTCPReadinessProbe(portName string) *TCPReadinessProbe {
	return &TCPReadinessProbe{
		portName: portName,
	}
}

func (p *TCPReadinessProbe) Ready(runnable Runnable) (err error) {
	endpoint := runnable.Endpoint(p.portName)
	if endpoint == "" {
		return errors.Newf("cannot get service endpoint for port %s", p.portName)
	} else if endpoint == "stopped" {
		return errors.New("service has stopped")
	}

	conn, err := net.DialTimeout("tcp", endpoint, time.Second)
	if err != nil {
		return err
	}

	return conn.Close()
}

// CmdReadinessProbe checks readiness by `Exec`ing a command (within container) which returns 0 to consider status being ready.
type CmdReadinessProbe struct {
	cmd Command
}

func NewCmdReadinessProbe(cmd Command) *CmdReadinessProbe {
	return &CmdReadinessProbe{cmd: cmd}
}

func (p *CmdReadinessProbe) Ready(runnable Runnable) error {
	return runnable.Exec(p.cmd)
}
