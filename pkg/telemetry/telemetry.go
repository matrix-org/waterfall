package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const PACKAGE = "waterfall"

var tracer = otel.Tracer(PACKAGE)

type Telemetry struct {
	span    trace.Span
	context context.Context //nolint:containedctx
}

func NewTelemetry(ctx context.Context, name string, attributes ...attribute.KeyValue) *Telemetry {
	ctx, span := tracer.Start(ctx, name, trace.WithAttributes(attributes...))

	return &Telemetry{
		span:    span,
		context: ctx,
	}
}

func (t *Telemetry) CreateChild(name string, attributes ...attribute.KeyValue) *Telemetry {
	return NewTelemetry(t.context, name, attributes...)
}

func (t *Telemetry) AddEvent(text string, attributes ...attribute.KeyValue) {
	traceAttributes := trace.WithAttributes(attributes...)
	t.span.AddEvent(text, traceAttributes)
}

func (t *Telemetry) AddError(err error) {
	t.span.RecordError(err)
}

func (t *Telemetry) Fail(err error) {
	t.span.SetStatus(codes.Error, err.Error())
	t.AddError(err)
}

func (t *Telemetry) End() {
	t.span.End()
}
