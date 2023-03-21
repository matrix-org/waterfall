package telemetry

type Config struct {
	// Use OTLP exporter. Has precedence over the Jaeger configuration.
	OTLP OTLP `yaml:"otlp"`
	// The URL to the Jaeger instance.
	JaegerURL string `yaml:"jaegerUrl"`
	// The package name to use for the telemetry.
	Package string `yaml:"package"`
	// ID of the service instance.
	ID string `yaml:"id"`
}

type OTLP struct {
	// The endpoint of the OTLP. Note that the endpoint must not contain any URL path.
	Host string `yaml:"host"`
	// Secure indicates whether to use TLS when connecting to the OTLP endpoint.
	// HTTPS is used if enabled, HTTP otherwise.
	Secure bool `yaml:"secure"`
}
