package webrtc_ext

// Configuration of the WebRTC API for the SFU.
type Config struct {
	// Enable simulcast extension.
	EnableSimulcast bool `yaml:"simulcast"`
	// Pulibc IP address of the SFU.
	PublicIPs []string `yaml:"ipAddresses"`
}
