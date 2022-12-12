package conference

type PingPongConfig struct {
	// Timeout for WebRTC connections. If the client doesn't respond to an
	// `m.call.ping` with an `m.call.pong` for this amount of time, the
	// connection is considered dead. (in seconds, no greater then 30)
	Timeout int `yaml:"timeout"`
	// The interval at which to send another m.call.ping event to the client.
	// (in seconds, greater then 30)
	Interval int `yaml:"interval"`
}

// Configuration for the group conferences (calls).
type Config struct {
	PingPongConfig PingPongConfig `yaml:"pingPong"`
}
