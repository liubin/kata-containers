// Copyright (c) 2020 Ant Group
//
// SPDX-License-Identifier: Apache-2.0
//

package virtcontainers

import (
	"fmt"
	otelTrace "go.opentelemetry.io/otel/trace"
)

const (
	supportedVersion  = 0
	traceparentHeader = "traceparent"
)

// traceParent get traceparent from span context for spans propagation
// the format is "00-44870b12565df07ab33db567d9cc8b6d-83d09266a03a99c9-01"
// https://github.com/kata-containers/kata-containers/blob/1ab64e30aa5ffedeebd534948376091c605ffff8/src/runtime/vendor/go.opentelemetry.io/otel/propagation/trace_context.go#L63-L68
func traceParent(span otelTrace.Span) string {
	sc := span.SpanContext()

	// Clear all flags other than the trace-context supported sampling bit.
	flags := sc.TraceFlags & otelTrace.FlagsSampled

	return fmt.Sprintf("%.2x-%s-%s-%.2x",
		supportedVersion,
		sc.TraceID,
		sc.SpanID,
		flags)
}
