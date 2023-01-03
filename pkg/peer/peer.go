package peer

import (
	"errors"
	"fmt"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/matrix-org/waterfall/pkg/peer/state"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
)

var (
	ErrCantCreatePeerConnection   = errors.New("can't create peer connection")
	ErrCantSetRemoteDescription   = errors.New("can't set remote description")
	ErrCantCreateAnswer           = errors.New("can't create answer")
	ErrCantSetLocalDescription    = errors.New("can't set local description")
	ErrCantCreateLocalDescription = errors.New("can't create local description")
	ErrDataChannelNotAvailable    = errors.New("data channel is not available")
	ErrDataChannelNotReady        = errors.New("data channel is not ready")
	ErrCantSubscribeToTrack       = errors.New("can't subscribe to track")
	ErrTrackNotFound              = errors.New("track not found")
)

// A wrapped representation of the peer connection (single peer in the call).
// The peer gets information about the things happening outside via public methods
// and informs the outside world about the things happening inside the peer by posting
// the messages to the channel.
type Peer[ID comparable] struct {
	logger         *logrus.Entry
	peerConnection *webrtc.PeerConnection
	sink           *common.MessageSink[ID, MessageContent]
	state          *state.PeerState
}

// Instantiates a new peer with a given SDP offer and returns a peer and the SDP answer if everything is ok.
func NewPeer[ID comparable](
	sdpOffer string,
	sink *common.MessageSink[ID, MessageContent],
	logger *logrus.Entry,
) (*Peer[ID], *webrtc.SessionDescription, error) {
	peerConnection, err := createPeerConnection()
	if err != nil {
		logger.WithError(err).Error("failed to create peer connection")
		return nil, nil, ErrCantCreatePeerConnection
	}

	peer := &Peer[ID]{
		logger:         logger,
		peerConnection: peerConnection,
		sink:           sink,
		state:          state.NewPeerState(),
	}

	peerConnection.OnTrack(peer.onRtpTrackReceived)
	peerConnection.OnDataChannel(peer.onDataChannelReady)
	peerConnection.OnICECandidate(peer.onICECandidateGathered)
	peerConnection.OnNegotiationNeeded(peer.onNegotiationNeeded)
	peerConnection.OnICEConnectionStateChange(peer.onICEConnectionStateChanged)
	peerConnection.OnICEGatheringStateChange(peer.onICEGatheringStateChanged)
	peerConnection.OnConnectionStateChange(peer.onConnectionStateChanged)
	peerConnection.OnSignalingStateChange(peer.onSignalingStateChanged)

	if sdpAnswer, err := peer.ProcessSDPOffer(sdpOffer); err != nil {
		return nil, nil, err
	} else {
		return peer, sdpAnswer, nil
	}
}

// Closes peer connection. From this moment on, no new messages will be sent from the peer.
func (p *Peer[ID]) Terminate() {
	if err := p.peerConnection.Close(); err != nil {
		p.logger.WithError(err).Error("failed to close peer connection")
	}

	// We want to seal the channel since the sender is not interested in us anymore.
	// We may want to remove this logic if/once we want to receive messages (confirmation of close or whatever)
	// from the peer that is considered closed.
	p.sink.Seal()
}

// Adds given tracks to our peer connection, so that they can be sent to the remote peer.
func (p *Peer[ID]) SubscribeTo(track common.TrackInfo) *Subscription {
	connection := NewConnectionWrapper(p.peerConnection, func(ti common.TrackInfo) {
		p.sink.Send(KeyFrameRequestReceived{ti})
	})

	subscription, err := NewSubscription(track, connection)
	if err != nil {
		p.logger.Errorf("Failed to subscribe to track: %s", err)
		return nil
	}

	p.logger.Infof("Subscribed to track: %s (%s)", track.TrackID, track.Layer.String())
	return subscription
}

// Writes the specified packets to the `trackID`.
func (p *Peer[ID]) RequestKeyFrame(info common.TrackInfo) error {
	// Find the right track.
	track := p.state.GetRemoteTrack(info.TrackID, info.Layer)
	if track == nil {
		return ErrTrackNotFound
	}

	// The ssrc that we must use when sending the RTCP packet.
	// Otherwise the peer won't understand where the packet comes from.
	ssrc := uint32(track.SSRC())

	rtcps := []rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: ssrc}}
	return p.peerConnection.WriteRTCP(rtcps)
}

// Tries to send the given message to the remote counterpart of our peer.
func (p *Peer[ID]) SendOverDataChannel(json string) error {
	dataChannel := p.state.GetDataChannel()
	if dataChannel == nil {
		return ErrDataChannelNotAvailable
	}

	if dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return ErrDataChannelNotReady
	}

	if err := dataChannel.SendText(json); err != nil {
		return fmt.Errorf("failed to send data over data channel: %w", err)
	}

	return nil
}

// Processes the remote ICE candidates.
func (p *Peer[ID]) ProcessNewRemoteCandidates(candidates []webrtc.ICECandidateInit) {
	for _, candidate := range candidates {
		if err := p.peerConnection.AddICECandidate(candidate); err != nil {
			p.logger.WithError(err).Error("failed to add ICE candidate")
		}
	}
}

// Processes the SDP answer received from the remote peer.
func (p *Peer[ID]) ProcessSDPAnswer(sdpAnswer string) error {
	err := p.peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  sdpAnswer,
	})
	if err != nil {
		p.logger.WithError(err).Error("failed to set remote description")
		return ErrCantSetRemoteDescription
	}

	return nil
}

// Applies the sdp offer received from the remote peer and generates an SDP answer.
func (p *Peer[ID]) ProcessSDPOffer(sdpOffer string) (*webrtc.SessionDescription, error) {
	err := p.peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  sdpOffer,
	})
	if err != nil {
		p.logger.WithError(err).Error("failed to set remote description")
		return nil, ErrCantSetRemoteDescription
	}

	answer, err := p.peerConnection.CreateAnswer(nil)
	if err != nil {
		p.logger.WithError(err).Error("failed to create answer")
		return nil, ErrCantCreateAnswer
	}

	if err := p.peerConnection.SetLocalDescription(answer); err != nil {
		p.logger.WithError(err).Error("failed to set local description")
		return nil, ErrCantSetLocalDescription
	}

	return &answer, nil
}
