// Copyright (c) 2018 Intel Corporation
//
// SPDX-License-Identifier: Apache-2.0
//

package katautils

import (
	"context"
	"fmt"
	"log"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/trace/jaeger"
	"go.opentelemetry.io/otel/label"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	otelTrace "go.opentelemetry.io/otel/trace"
)

// Implements jaeger-client-go.Logger interface
type traceLogger struct {
}

// tracerCloser contains a copy of the closer returned by createTracer() which
// is used by stopTracing().
var tracerCloser func()

func (t traceLogger) Error(msg string) {
	kataUtilsLogger.Error(msg)
}

func (t traceLogger) Infof(msg string, args ...interface{}) {
	kataUtilsLogger.Infof(msg, args...)
}

func InitTracing(name string) (func(), error) {
	host := "30.30.215.12"
	// Create and install Jaeger export pipeline.
	flush, err := jaeger.InstallNewPipeline(
		jaeger.WithCollectorEndpoint(fmt.Sprintf("http://%s:14268/api/traces", host)),
		jaeger.WithProcess(jaeger.Process{
			ServiceName: name,
			Tags: []label.KeyValue{
				label.String("exporter", "jaeger"),
				label.String("lib", "opentelemetry"),
			},
		}),
		jaeger.WithSDK(&sdktrace.Config{DefaultSampler: sdktrace.AlwaysSample()}),
	)
	if err != nil {
		log.Fatal(err)
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	tracerCloser = flush
	return flush, nil
}

// StopTracing ends all tracing, reporting the spans to the collector.
func StopTracing(ctx context.Context) {
	if !tracing {
		return
	}

	// report all possible spans to the collector
	if tracerCloser != nil {
		tracerCloser()
	}
}

// Trace creates a new tracing span based on the specified name and parent
// context.
func Trace(parent context.Context, name string) (otelTrace.Span, context.Context) {

	tracer := otel.Tracer("kata")
	ctx, span := tracer.Start(parent, name)

	span.SetAttributes(label.Key("source").String("runtime"))

	// This is slightly confusing: when tracing is disabled, trace spans
	// are still created - but the tracer used is a NOP. Therefore, only
	// display the message when tracing is really enabled.
	if tracing {
		// This log message is *essential*: it is used by:
		// https: //github.com/kata-containers/tests/blob/master/tracing/tracing-test.sh
		kataUtilsLogger.Debugf("created span %v", span)
	}

	return span, ctx
}
