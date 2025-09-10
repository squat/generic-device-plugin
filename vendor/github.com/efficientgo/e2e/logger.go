// Copyright (c) The EfficientGo Authors.
// Licensed under the Apache License 2.0.

package e2e

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// Logger is the fundamental interface for all log operations. Log creates a
// log event from keyvals, a variadic sequence of alternating keys and values.
// Implementations must be safe for concurrent use by multiple goroutines. In
// particular, any implementation of Logger that appends to keyvals or
// modifies or retains any of its elements must make a copy first.
// This is 1:1 copy of "github.com/go-kit/kit/log" interface.
type Logger interface {
	Log(keyvals ...interface{}) error
}

var _ Logger = &SimpleLogger{}

type SimpleLogger struct {
	w io.Writer
}

func NewLogger(w io.Writer) *SimpleLogger {
	return &SimpleLogger{
		w: w,
	}
}

func (l *SimpleLogger) Log(keyvals ...interface{}) error {
	b := strings.Builder{}
	b.WriteString(time.Now().Format("15:04:05"))

	for _, v := range keyvals {
		b.WriteString(" " + fmt.Sprintf("%v", v))
	}

	b.WriteString("\n")

	_, err := l.w.Write([]byte(b.String()))
	return err
}

type LinePrefixLogger struct {
	prefix string
	logger Logger
}

func (w *LinePrefixLogger) Write(p []byte) (n int, err error) {
	for _, line := range strings.Split(string(p), "\n") {
		// Skip empty lines.
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Write the prefix + line to the wrapped writer.
		if err := w.logger.Log(w.prefix + line); err != nil {
			return 0, err
		}
	}

	return len(p), nil
}
