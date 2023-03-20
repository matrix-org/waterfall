package telemetry

type Config struct {
	// The URL to the Jaeger instance.
	JaegerURL string `yaml:"jaegerUrl"`
	// The package name to use for the telemetry.
	Package string `yaml:"package"`
	// ID of the service instance.
	ID string `yaml:"id"`
}
