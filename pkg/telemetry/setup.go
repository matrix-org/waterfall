package telemetry

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

// A simple helper that configures OpenTelemetry for the SFU.
func SetupTelemetry(config Config) (*tracesdk.TracerProvider, error) {
	// Create a new resource.
	res, err := NewResource(config.Package, config.ID)
	if err != nil {
		return nil, err
	}

	// Create the exporter depending on the configuration from the user.
	exp, expErr := func() (tracesdk.SpanExporter, error) {
		switch {
		case config.OTLP.Host != "":
			return NewOTLPExporter(config.OTLP)
		case config.JaegerURL != "":
			return jaeger.New(jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(config.JaegerURL)))
		default:
			return nil, fmt.Errorf("neither OTLP nor Jaeger URL is set")
		}
	}()

	if expErr != nil {
		return nil, expErr
	}

	// Create a new trace provider.
	tp := NewTracerProvider(exp, res)

	// Set the trace provider as the global trace provider.
	otel.SetTracerProvider(tp)

	// Context propagation for the OpenTelemetry SDK.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tp, nil
}

// Creates a trace provider - an entity that manages the puts together OTel things,
// i.e. it essentially allows to set a "global logger" for the whole application.
// Under the hood it creates span processors, i.e. hooks that receive all the events
// and write them to the exporters (e.g. Jaeger) while associating each of them with
// our service.
func NewTracerProvider(exp tracesdk.SpanExporter, res *resource.Resource) *tracesdk.TracerProvider {
	// Create a trace provider with the Jaeger exporter.
	tp := tracesdk.NewTracerProvider(
		tracesdk.WithSampler(tracesdk.AlwaysSample()),
		tracesdk.WithBatcher(exp),
		tracesdk.WithResource(res),
	)

	return tp
}

// Creates a new resource to identify the service instance.
func NewResource(pkg, identifier string) (*resource.Resource, error) {
	if pkg == "" || identifier == "" {
		return nil, fmt.Errorf("empty resource name or identifier")
	}

	res, err := resource.New(
		context.Background(),
		resource.WithContainer(),
		resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceName(pkg),
			attribute.String("ID", identifier),
		),
	)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// Creates a new OTLP exporter.
func NewOTLPExporter(config OTLP) (*otlptrace.Exporter, error) {
	// The requirements for the endpoint of the `otlptracehttp` are not enforced when
	// you pass the option to the constructor. So we have to check it manually. Otherwise
	// it'll fail once we start sending traces which is too late it's not something that
	// we can detect since the error is not returned, but **logged** in stdout.
	switch {
	case config.Host == "":
		return nil, fmt.Errorf("OTLP host is not set")
	case strings.HasPrefix(config.Host, "http://"):
		return nil, fmt.Errorf("OTLP host must not contain the protocol")
	case strings.HasSuffix(config.Host, "/"):
		return nil, fmt.Errorf("OTLP host must not contain the path or trailing slashes")
	}

	options := []otlptracehttp.Option{otlptracehttp.WithEndpoint(config.Host)}
	if !config.Secure {
		options = append(options, otlptracehttp.WithInsecure())
	}

	return otlptrace.New(context.Background(), otlptracehttp.NewClient(options...))
}
