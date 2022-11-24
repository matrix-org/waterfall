package peer

import (
	"errors"
	"sync"

	"github.com/matrix-org/waterfall/src/common"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
)

var (
	ErrCantCreatePeerConnection   = errors.New("can't create peer connection")
	ErrCantSetRemoteDecsription   = errors.New("can't set remote description")
	ErrCantCreateAnswer           = errors.New("can't create answer")
	ErrCantSetLocalDescription    = errors.New("can't set local description")
	ErrCantCreateLocalDescription = errors.New("can't create local description")
	ErrDataChannelNotAvailable    = errors.New("data channel is not available")
	ErrDataChannelNotReady        = errors.New("data channel is not ready")
	ErrCantSubscribeToTrack       = errors.New("can't subscribe to track")
)

type Peer[ID comparable] struct {
	logger         *logrus.Entry
	peerConnection *webrtc.PeerConnection
	sink           *common.MessageSink[ID, MessageContent]

	dataChannelMutex sync.Mutex
	dataChannel      *webrtc.DataChannel
}

func NewPeer[ID comparable](
	sdpOffer string,
	sink *common.MessageSink[ID, MessageContent],
	logger *logrus.Entry,
) (*Peer[ID], *webrtc.SessionDescription, error) {
	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		logger.WithError(err).Error("failed to create peer connection")
		return nil, nil, ErrCantCreatePeerConnection
	}

	peer := &Peer[ID]{
		logger:         logger,
		peerConnection: peerConnection,
		sink:           sink,
	}

	peerConnection.OnTrack(peer.onRtpTrackReceived)
	peerConnection.OnDataChannel(peer.onDataChannelReady)
	peerConnection.OnICECandidate(peer.onICECandidateGathered)
	peerConnection.OnNegotiationNeeded(peer.onNegotiationNeeded)
	peerConnection.OnICEConnectionStateChange(peer.onICEConnectionStateChanged)
	peerConnection.OnICEGatheringStateChange(peer.onICEGatheringStateChanged)
	peerConnection.OnConnectionStateChange(peer.onConnectionStateChanged)
	peerConnection.OnSignalingStateChange(peer.onSignalingStateChanged)

	err = peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  sdpOffer,
	})
	if err != nil {
		logger.WithError(err).Error("failed to set remote description")
		peerConnection.Close()
		return nil, nil, ErrCantSetRemoteDecsription
	}

	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		logger.WithError(err).Error("failed to create answer")
		peerConnection.Close()
		return nil, nil, ErrCantCreateAnswer
	}

	if err := peerConnection.SetLocalDescription(answer); err != nil {
		logger.WithError(err).Error("failed to set local description")
		peerConnection.Close()
		return nil, nil, ErrCantSetLocalDescription
	}

	// TODO: Do we really need to call `webrtc.GatheringCompletePromise`
	//       as in the previous version of the `waterfall` here?

	sdpAnswer := peerConnection.LocalDescription()
	if sdpAnswer == nil {
		logger.WithError(err).Error("could not generate a local description")
		peerConnection.Close()
		return nil, nil, ErrCantCreateLocalDescription
	}

	return peer, sdpAnswer, nil
}

func (p *Peer[ID]) Terminate() {
	if err := p.peerConnection.Close(); err != nil {
		p.logger.WithError(err).Error("failed to close peer connection")
	}

	p.sink.Send(LeftTheCall{})
}

func (p *Peer[ID]) AddICECandidates(candidates []webrtc.ICECandidateInit) {
	for _, candidate := range candidates {
		if err := p.peerConnection.AddICECandidate(candidate); err != nil {
			p.logger.WithError(err).Error("failed to add ICE candidate")
		}
	}
}

func (p *Peer[ID]) SubscribeToTrack(track *webrtc.TrackLocalStaticRTP) error {
	_, err := p.peerConnection.AddTrack(track)
	if err != nil {
		p.logger.WithError(err).Error("failed to add track")
		return ErrCantSubscribeToTrack
	}

	return nil
}

func (p *Peer[ID]) SendOverDataChannel(json string) error {
	p.dataChannelMutex.Lock()
	defer p.dataChannelMutex.Unlock()

	if p.dataChannel == nil {
		p.logger.Error("can't send data over data channel: data channel is not ready")
		return ErrDataChannelNotAvailable
	}

	if p.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		p.logger.Error("can't send data over data channel: data channel is not open")
		return ErrDataChannelNotReady
	}

	if err := p.dataChannel.SendText(json); err != nil {
		p.logger.WithError(err).Error("failed to send data over data channel")
	}

	return nil
}

func (p *Peer[ID]) NewSDPAnswerReceived(sdpAnswer string) error {
	err := p.peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  sdpAnswer,
	})
	if err != nil {
		p.logger.WithError(err).Error("failed to set remote description")
		return ErrCantSetRemoteDecsription
	}

	return nil
}
