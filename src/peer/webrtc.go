package peer

import (
	"errors"
	"io"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

// A callback that is called once we receive first RTP packets from a track, i.e.
// we call this function each time a new track is received.
func (p *Peer) onRtpTrackReceived(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
	// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval.
	// This can be less wasteful by processing incoming RTCP events, then we would emit a NACK/PLI
	// when a viewer requests it.
	//
	// TODO: Add RTCP handling based on the PR from @SimonBrandner.
	go func() {
		ticker := time.NewTicker(time.Millisecond * 500) // every 500ms
		for range ticker.C {
			rtcp := []rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(remoteTrack.SSRC())}}
			if rtcpSendErr := p.peerConnection.WriteRTCP(rtcp); rtcpSendErr != nil {
				p.logger.Errorf("Failed to send RTCP PLI: %v", rtcpSendErr)
			}
		}
	}()

	// Create a local track, all our SFU clients that are subscribed to this
	// peer (publisher) wil be fed via this track.
	localTrack, err := webrtc.NewTrackLocalStaticRTP(
		remoteTrack.Codec().RTPCodecCapability,
		remoteTrack.ID(),
		remoteTrack.StreamID(),
	)
	if err != nil {
		p.logger.WithError(err).Error("failed to create local track")
		return
	}

	// Notify others that our track has just been published.
	p.notify <- NewTrackPublished{Sender: p.id, Track: localTrack}

	// Start forwarding the data from the remote track to the local track,
	// so that everyone who is subscribed to this track will receive the data.
	go func() {
		rtpBuf := make([]byte, 1400)

		for {
			index, _, readErr := remoteTrack.Read(rtpBuf)
			// TODO: inform the conference that this publisher's track is not available anymore.
			if readErr != nil {
				if readErr == io.EOF { // finished, no more data, no error, inform others
					p.logger.Info("remote track closed")
				} else { // finished, no more data, but with error, inform others
					p.logger.WithError(readErr).Error("failed to read from remote track")
				}
				p.notify <- PublishedTrackFailed{Sender: p.id, Track: localTrack}
			}

			// ErrClosedPipe means we don't have any subscribers, this is ok if no peers have connected yet.
			if _, err = localTrack.Write(rtpBuf[:index]); err != nil && !errors.Is(err, io.ErrClosedPipe) {
				p.logger.WithError(err).Error("failed to write to local track")
				p.notify <- PublishedTrackFailed{Sender: p.id, Track: localTrack}
			}
		}
	}()
}

// A callback that is called once we receive an ICE candidate for this peer connection.
func (p *Peer) onICECandidateGathered(candidate *webrtc.ICECandidate) {
	if candidate == nil {
		p.logger.Info("ICE candidate gathering finished")
		return
	}

	p.logger.WithField("candidate", candidate).Debug("ICE candidate gathered")
	p.notify <- NewICECandidate{Sender: p.id, Candidate: candidate}
}

// A callback that is called once we receive an ICE connection state change for this peer connection.
func (p *Peer) onNegotiationNeeded() {
	p.logger.Debug("negotiation needed")
	offer, err := p.peerConnection.CreateOffer(nil)
	if err != nil {
		p.logger.WithError(err).Error("failed to create offer")
		return
	}

	if err := p.peerConnection.SetLocalDescription(offer); err != nil {
		p.logger.WithError(err).Error("failed to set local description")
		return
	}

	p.notify <- NewOffer{Sender: p.id, Offer: &offer}
}

// A callback that is called once we receive an ICE connection state change for this peer connection.
func (p *Peer) onICEConnectionStateChanged(state webrtc.ICEConnectionState) {
	p.logger.WithField("state", state).Debug("ICE connection state changed")

	switch state {
	case webrtc.ICEConnectionStateFailed, webrtc.ICEConnectionStateDisconnected:
		// TODO: We may want to treat it as an opportunity for the ICE restart instead.
		// TODO: Ask Simon if we should do it here as in the previous implementation of the
		//      `waterfall` or the way I did it in this new implementation.
		// p.notify <- PeerLeftTheCall{sender: p.data}
	case webrtc.ICEConnectionStateCompleted, webrtc.ICEConnectionStateConnected:
		// TODO: Start keep-alive timer over the data channel to check the connecitons that hanged.
		// TODO: Ask Simon if we should do it here as in the previous implementation of the
		//       `waterfall` or the way I did it in this new implementation.
		// p.notify <- PeerJoinedTheCall{sender: p.data}
		p.notify <- ICEGatheringComplete{Sender: p.id}
	}
}

func (p *Peer) onICEGatheringStateChanged(state webrtc.ICEGathererState) {
	p.logger.WithField("state", state).Debug("ICE gathering state changed")

	if state == webrtc.ICEGathererStateComplete {
		p.notify <- ICEGatheringComplete{Sender: p.id}
	}
}

func (p *Peer) onSignalingStateChanged(state webrtc.SignalingState) {
	p.logger.WithField("state", state).Debug("signaling state changed")
}

func (p *Peer) onConnectionStateChanged(state webrtc.PeerConnectionState) {
	p.logger.WithField("state", state).Debug("connection state changed")

	switch state {
	case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateDisconnected, webrtc.PeerConnectionStateClosed:
		p.notify <- PeerLeftTheCall{Sender: p.id}
	case webrtc.PeerConnectionStateConnected:
		p.notify <- PeerJoinedTheCall{Sender: p.id}
	}
}

// A callback that is called once the data channel is ready to be used.
func (p *Peer) onDataChannelReady(dc *webrtc.DataChannel) {
	p.dataChannelMutex.Lock()
	defer p.dataChannelMutex.Unlock()

	if p.dataChannel != nil {
		p.logger.Error("data channel already exists")
		p.dataChannel.Close()
		return
	}

	p.dataChannel = dc
	p.logger.WithField("label", dc.Label()).Info("data channel ready")

	dc.OnOpen(func() {
		p.logger.Info("data channel opened")
		p.notify <- DataChannelOpened{Sender: p.id}
	})

	dc.OnClose(func() {
		p.logger.Info("data channel closed")
		p.notify <- DataChannelClosed{Sender: p.id}
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		p.logger.WithField("message", msg).Debug("data channel message received")
		if msg.IsString {
			p.notify <- DataChannelMessage{Sender: p.id, Message: string(msg.Data)}
		} else {
			p.logger.Warn("data channel message is not a string, ignoring")
		}
	})

	dc.OnError(func(err error) {
		p.logger.WithError(err).Error("data channel error")
		p.notify <- DataChannelError{Sender: p.id, Err: err}
	})
}
