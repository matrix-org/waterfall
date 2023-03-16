package telemetry

import "go.opentelemetry.io/otel"

const PACKAGE = "waterfall"

var TRACER = otel.Tracer(PACKAGE)
