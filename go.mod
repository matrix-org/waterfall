module github.com/matrix-org/waterfall

go 1.19

require github.com/pion/webrtc/v3 v3.1.31

require (
	github.com/pion/interceptor v0.1.10
	github.com/pion/rtcp v1.2.9
	github.com/pion/rtp v1.7.13
	github.com/sirupsen/logrus v1.9.0
	golang.org/x/exp v0.0.0-20230116083435-1de6713980de
	gopkg.in/yaml.v3 v3.0.1
	maunium.net/go/mautrix v0.11.0
)

require (
	github.com/google/uuid v1.3.0 // indirect
	github.com/pion/datachannel v1.5.2 // indirect
	github.com/pion/dtls/v2 v2.2.4 // indirect
	github.com/pion/ice/v2 v2.2.3 // indirect
	github.com/pion/logging v0.2.2 // indirect
	github.com/pion/mdns v0.0.5 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/sctp v1.8.2 // indirect
	github.com/pion/sdp/v3 v3.0.4 // indirect
	github.com/pion/srtp/v2 v2.0.5 // indirect
	github.com/pion/stun v0.3.5 // indirect
	github.com/pion/transport v0.13.0 // indirect
	github.com/pion/transport/v2 v2.0.0 // indirect
	github.com/pion/turn/v2 v2.0.8 // indirect
	github.com/pion/udp v0.1.4 // indirect
	github.com/tidwall/gjson v1.14.1 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.0 // indirect
	github.com/tidwall/sjson v1.2.4 // indirect
	golang.org/x/crypto v0.5.0 // indirect
	golang.org/x/net v0.7.0 // indirect
	golang.org/x/sys v0.5.0 // indirect
)

replace maunium.net/go/mautrix => github.com/matrix-org/mautrix-go v0.0.0-20221213094344-43c13b516216
