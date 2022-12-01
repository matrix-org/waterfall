package conference

// Configuration for the group conferences (calls).
type Config struct {
	// Keep-alive timeout for WebRTC connections. If no keep-alive has been received
	// from the client for this duration, the connection is considered dead (in seconds).
	KeepAliveTimeout int `yaml:"timeout"`
}
