package conference

// Configuration for the group conferences (calls).
type Config struct {
	// Keep-alive timeout for WebRTC connections. If the client doesn't respond
	// to an `m.call.ping` with an `m.call.pong` for this amount of time, the
	// connection is considered dead. (in seconds, no greater then 30)
	KeepAliveTimeout int `yaml:"timeout"`
	// The time after which we should send another m.call.ping event to the
	// client. (in seconds, greater then 30)
	PingInterval int `yaml:"pingInterval"`
}
