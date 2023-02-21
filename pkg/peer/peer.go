package peer

import (
	"errors"

	"github.com/matrix-org/waterfall/pkg/channel"
	"github.com/matrix-org/waterfall/pkg/peer/state"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/matrix-org/waterfall/pkg/worker"
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
	ErrDataChannelNotReady        = errors.New("data channel is not ready")
	ErrCantSubscribeToTrack       = errors.New("can't subscribe to track")
)

// A wrapped representation of the peer connection (single peer in the call).
// The peer gets information about the things happening outside via public methods
// and informs the outside world about the things happening inside the peer by posting
// the messages to the channel.
type Peer[ID comparable] struct {
	logger            *logrus.Entry
	peerConnection    *webrtc.PeerConnection
	sink              *channel.SinkWithSender[ID, MessageContent]
	state             *state.PeerState
	dataChannelWorker *worker.Worker[string]
}

// Instantiates a new peer with a given SDP offer and returns a peer and the SDP answer if everything is ok.
func NewPeer[ID comparable](
	connectionFactory *webrtc_ext.PeerConnectionFactory,
	sdpOffer string,
	sink *channel.SinkWithSender[ID, MessageContent],
	logger *logrus.Entry,
) (*Peer[ID], *webrtc.SessionDescription, error) {
	peerConnection, err := connectionFactory.CreatePeerConnection()
	if err != nil {
		logger.WithError(err).Error("failed to create peer connection")
		return nil, nil, ErrCantCreatePeerConnection
	}

	// The thread-safe peer state.
	peerState := state.NewPeerState()

	// The worker that is responsible for writing data channel messages.
	dataChannelWorker := newDataChannelWorker(peerState, logger)

	peer := &Peer[ID]{
		logger:            logger,
		peerConnection:    peerConnection,
		sink:              sink,
		state:             peerState,
		dataChannelWorker: dataChannelWorker,
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

	// Stop the worker for the data channel messages.
	p.dataChannelWorker.Stop()
}

// Request a key frame from the peer connection.
func (p *Peer[ID]) RequestKeyFrame(track *webrtc.TrackRemote) error {
	rtcps := []rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}}
	return p.peerConnection.WriteRTCP(rtcps)
}

// Implementation of the `SubscriptionController` interface.
func (p *Peer[ID]) AddTrack(track *webrtc.TrackLocalStaticRTP) (*webrtc.RTPSender, error) {
	return p.peerConnection.AddTrack(track)
}

// Implementation of the `SubscriptionController` interface.
func (p *Peer[ID]) RemoveTrack(sender *webrtc.RTPSender) error {
	return p.peerConnection.RemoveTrack(sender)
}

// Tries to send the given message to the remote counterpart of our peer.
// The error returned from this function means that the message has not been sent.
// Note that if no error is returned, it doesn't mean that the message has been
// successfully sent. It only means that the message has been "scheduled" (enqueued).
func (p *Peer[ID]) SendOverDataChannel(json string) error {
	// Preliminary quick check, so that we can fail early if the channel is closed.
	if ch := p.state.GetDataChannel(); ch != nil && ch.ReadyState() == webrtc.DataChannelStateOpen {
		// Note that by this moment the channel could have closed, so the fact that
		// we successfully enqueue the message doesn't mean that it will be sent or delivered.
		return p.dataChannelWorker.Send(json)
	}

	return ErrDataChannelNotReady
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
