package signaling

import "maunium.net/go/mautrix/id"

// Configuration for the Matrix client.
type Config struct {
	// The Matrix ID (MXID) of the SFU.
	UserID id.UserID `yaml:"userid"`
	// The ULR of the homeserver that SFU talks to.
	HomeserverURL string `yaml:"homeserverurl"`
	// The access token for the Matrix SDK.
	AccessToken string `yaml:"accesstoken"`
}
