package telemetry

import (
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

// A simple helper that configures OpenTelemetry for the SFU.
func SetupTelemetry(url string) (*tracesdk.TracerProvider, error) {
	// Create a new resource.
	res, err := NewResource()
	if err != nil {
		return nil, err
	}

	// Create a new Jaeger exporter.
	exp, err := NewJaegerExporter(url)
	if err != nil {
		return nil, err
	}

	// Create a new trace provider.
	tp := NewTracerProvider(exp, res)

	// Set the trace provider as the global trace provider.
	otel.SetTracerProvider(tp)
	tracer = otel.Tracer(PACKAGE)

	// Context propagation for the OpenTelemetry SDK.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tp, nil
}

// Creates a trace provider - an entity that manages the puts together OTel things,
// i.e. it essentially allows to set a "global logger" for the whole application.
// Under the hood it creates span processors, i.e. hooks that receive all the events
// and write them to the exporters (e.g. Jaeger) while associating each of them with
// our service.
func NewTracerProvider(exp *jaeger.Exporter, res *resource.Resource) *tracesdk.TracerProvider {
	// Create a trace provider with the Jaeger exporter.
	tp := tracesdk.NewTracerProvider(
		tracesdk.WithSampler(tracesdk.AlwaysSample()),
		tracesdk.WithBatcher(exp),
		tracesdk.WithResource(res),
	)

	return tp
}

// Creates Jaeger exporter.
func NewJaegerExporter(url string) (*jaeger.Exporter, error) {
	exp, err := jaeger.New(jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(url)))
	if err != nil {
		return nil, err
	}

	return exp, nil
}

// Creates a new resource to identify the service instance.
func NewResource() (*resource.Resource, error) {
	// Generate random string ID.
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}

	// TODO: Add the semver of the service here as well as the information about its environment.
	return resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(PACKAGE),
		attribute.String("ID", id.String()),
	), nil
}
