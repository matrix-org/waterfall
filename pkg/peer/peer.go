package peer

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
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

	dataChannelMutex sync.Mutex
	dataChannel      *webrtc.DataChannel
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
func (p *Peer[ID]) SubscribeTo(tracks []ExtendedTrackInfo) {
	for _, track := range tracks {
		// Set the RID if any (would be "" if no simulcast is used).
		setRid := webrtc.WithRTPStreamID(SimulcastLayerToRID(track.Layer))

		// Create a new track.
		rtpTrack, err := webrtc.NewTrackLocalStaticRTP(track.Codec, track.TrackID, track.StreamID, setRid)
		if err != nil {
			p.logger.Errorf("Failed to create track: %s", err)
			continue
		}

		rtpSender, err := p.peerConnection.AddTrack(rtpTrack)
		if err != nil {
			p.logger.Errorf("Failed to add track: %s", err)
			continue
		}

		// Start reading and forwarding RTP packets.
		go p.readRTCP(rtpSender)

		p.logger.Infof("Subscribed to track: %s", track.TrackID)
	}
}

// Unsubscribes from the given list of tracks.
func (p *Peer[ID]) UnsubscribeFrom(tracks []string) {
	// That's unfortunately an O(m*n) operation, but we don't expect the number of tracks to be big.
	for _, sender := range p.peerConnection.GetSenders() {
		currentTrack := sender.Track()
		if currentTrack == nil {
			continue
		}

		for _, trackToUnsubscribe := range tracks {
			presentTrackID, trackID := currentTrack.ID(), trackToUnsubscribe
			if presentTrackID == trackID {
				if err := p.peerConnection.RemoveTrack(sender); err != nil {
					p.logger.WithError(err).Error("failed to remove track")
				} else {
					p.logger.Infof("unsubscribed from track: %s", trackID)
				}
			}
		}
	}
}

// Writes an RTP packet to a given track.
func (p *Peer[ID]) WriteRTP(trackID string, packet *rtp.Packet) error {
	// Find the right track.
	senders := p.peerConnection.GetSenders()
	senderIndex := slices.IndexFunc(senders, func(sender *webrtc.RTPSender) bool {
		return sender.Track() != nil && sender.Track().ID() == trackID
	})
	if senderIndex == -1 {
		return ErrTrackNotFound
	}

	localTrack, ok := senders[senderIndex].Track().(*webrtc.TrackLocalStaticRTP)
	if !ok {
		return ErrTrackNotFound
	}

	if err := localTrack.WriteRTP(packet); err != nil {
		return err
	}

	return nil
}

// Writes the specified packets to the `trackID`.
func (p *Peer[ID]) WriteRTCP(trackID string, packets []RTCPPacket) error {
	// Find the right track.
	receivers := p.peerConnection.GetReceivers()
	receiverIndex := slices.IndexFunc(receivers, func(receiver *webrtc.RTPReceiver) bool {
		return receiver.Track() != nil && receiver.Track().ID() == trackID
	})
	if receiverIndex == -1 {
		return ErrTrackNotFound
	}

	// The ssrc that we must use when sending the RTCP packet.
	// Otherwise the peer won't understand where the packet comes from.
	ssrc := uint32(receivers[receiverIndex].Track().SSRC())

	toSend := make([]rtcp.Packet, len(packets))
	for i, packet := range packets {
		switch packet.Type {
		case PictureLossIndicator:
			// PLIs are trivial, they just have media SSRC and sender SSRC, where the last one
			// does not seem to matter (based on Pion examples of using these).
			toSend[i] = &rtcp.PictureLossIndication{MediaSSRC: ssrc}
		case FullIntraRequest:
			// FIRs are a bit more complicated. They have a sequence number that must be incremented
			// and an additional SSRC inside FIR payload. So we rewrite the media SSRC here.
			rewrittenFIR, _ := packet.Content.(*rtcp.FullIntraRequest)
			rewrittenFIR.MediaSSRC = ssrc
			// TODO: Check is we also need to rewrite the SSRC inside the FIR payload.
			toSend[i] = rewrittenFIR
		}
	}

	return p.peerConnection.WriteRTCP(toSend)
}

// Tries to send the given message to the remote counterpart of our peer.
func (p *Peer[ID]) SendOverDataChannel(json string) error {
	p.dataChannelMutex.Lock()
	defer p.dataChannelMutex.Unlock()

	if p.dataChannel == nil {
		return ErrDataChannelNotAvailable
	}

	if p.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return ErrDataChannelNotReady
	}

	if err := p.dataChannel.SendText(json); err != nil {
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

// Returns the information about the tracks that we're currently subscribed to.
func (p *Peer[ID]) GetSubscribedTracks() map[string]ExtendedTrackInfo {
	trackInfos := make(map[string]ExtendedTrackInfo, 0)
	for _, sender := range p.peerConnection.GetSenders() {
		if track, ok := sender.Track().(*webrtc.TrackLocalStaticRTP); ok {
			basicInfo := TrackInfo{
				TrackID:  track.ID(),
				StreamID: track.StreamID(),
				Codec:    track.Codec(),
			}

			trackInfos[track.ID()] = ExtendedTrackInfo{basicInfo, RIDToSimulcastLayer(track.RID())}
		}
	}

	return trackInfos
}

// Read incoming RTCP packets
// Before these packets are returned they are processed by interceptors. For things
// like NACK this needs to be called.
func (p *Peer[ID]) readRTCP(rtpSender *webrtc.RTPSender) {
	for {
		packets, _, err := rtpSender.ReadRTCP()
		if err != nil {
			if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, io.EOF) {
				p.logger.WithError(err).Warn("failed to read RTCP on track")
				return
			}
		}

		// We only want to inform others about PLIs and FIRs. We skip the rest of the packets for now.
		toForward := []RTCPPacket{}
		for _, packet := range packets {
			// TODO: Should we also handle NACKs?
			switch packet.(type) {
			case *rtcp.PictureLossIndication:
				toForward = append(toForward, RTCPPacket{PictureLossIndicator, packet})
			case *rtcp.FullIntraRequest:
				toForward = append(toForward, RTCPPacket{FullIntraRequest, packet})
			}
		}

		p.sink.Send(RTCPReceived{Packets: toForward, TrackID: rtpSender.Track().ID()})
	}
}
