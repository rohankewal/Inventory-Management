package logging

import "runtime"

// runtimeStack captures the current goroutine's stack, split out so the
// recovery path in logging.go stays readable.
func runtimeStack(buf []byte) int {
	return runtime.Stack(buf, false)
}
