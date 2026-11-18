package apiservertest

// client-go reports each failed watch through apimachinery's
// utilruntime.HandleError. Its default handlers log the error and then
// rate-limit: they record the time of the last error in a package
// variable and sleep until one millisecond after that time. The
// variable is first set at program start, and the clock of a synctest
// bubble starts at midnight UTC on 2000-01-01, which is earlier than a
// time recorded outside the bubble or late in an earlier bubble. So the
// first failed watch in a bubble can sleep for years of fake time. The
// reflector's goroutine is still asleep when the test ends, and the
// bubble deadlocks. Only
// tests import this package, so init keeps the logging and removes the
// rate limit.

import (
	"context"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
)

func init() {
	utilruntime.ErrorHandlers = []utilruntime.ErrorHandler{logUnhandled}
}

// logUnhandled logs like apimachinery's default logError handler.
func logUnhandled(ctx context.Context, err error, message string, keysAndValues ...any) {
	klog.LoggerWithName(klog.FromContext(ctx), "UnhandledError").Error(err, message, keysAndValues...)
}
