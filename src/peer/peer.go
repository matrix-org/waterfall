package peer

import (
	"errors"
	"sync"

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

type Peer struct {
	id             ID
	logger         *logrus.Entry
	notify         chan<- interface{}
	peerConnection *webrtc.PeerConnection

	dataChannelMutex sync.Mutex
	dataChannel      *webrtc.DataChannel
}

func NewPeer(
	info ID,
	conferenceId string,
	sdpOffer string,
	notify chan<- interface{},
) (*Peer, *webrtc.SessionDescription, error) {
	logger := logrus.WithFields(logrus.Fields{
		"user_id":   info.UserID,
		"device_id": info.DeviceID,
		"conf_id":   conferenceId,
	})

	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		logger.WithError(err).Error("failed to create peer connection")
		return nil, nil, ErrCantCreatePeerConnection
	}

	peer := &Peer{
		id:             info,
		logger:         logger,
		notify:         notify,
		peerConnection: peerConnection,
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

func (p *Peer) Terminate() {
	if err := p.peerConnection.Close(); err != nil {
		p.logger.WithError(err).Error("failed to close peer connection")
	}

	p.notify <- LeftTheCall{Sender: p.id}
}

func (p *Peer) AddICECandidates(candidates []webrtc.ICECandidateInit) {
	for _, candidate := range candidates {
		if err := p.peerConnection.AddICECandidate(candidate); err != nil {
			p.logger.WithError(err).Error("failed to add ICE candidate")
		}
	}
}

func (p *Peer) SubscribeToTrack(track *webrtc.TrackLocalStaticRTP) error {
	_, err := p.peerConnection.AddTrack(track)
	if err != nil {
		p.logger.WithError(err).Error("failed to add track")
		return ErrCantSubscribeToTrack
	}

	return nil
}

func (p *Peer) SendOverDataChannel(json string) error {
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

func (p *Peer) NewSDPAnswerReceived(sdpAnswer string) error {
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
