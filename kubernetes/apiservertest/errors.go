package apiservertest

// client-go reports each failed watch through apimachinery's
// utilruntime.HandleError, and the default handlers log the error and
// then run a global rate limit. The rate limit records the time of the
// last error in a package variable, first at program start, and sleeps
// until one millisecond after that time. Each bubble's clock starts at
// midnight UTC 2000-01-01, before any time the variable records outside
// a bubble or in an earlier bubble. So the first failed watch in a
// bubble sleeps for years of fake time, the reflector stops, and the
// bubble deadlocks when the test ends. A program that imports this
// package is a test, and in a test the rate limit protects nothing, so
// the package keeps the log and removes the rate limit.

import (
	"context"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
)

func init() {
	utilruntime.ErrorHandlers = []utilruntime.ErrorHandler{logUnhandled}
}

// logUnhandled logs the error the way apimachinery's own first handler
// does.
func logUnhandled(ctx context.Context, err error, message string, keysAndValues ...any) {
	klog.LoggerWithName(klog.FromContext(ctx), "UnhandledError").Error(err, message, keysAndValues...)
}
