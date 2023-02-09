package peer

import (
	"errors"
	"io"

	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

func (p *Peer[ID]) handleNewVideoTrack(
	trackInfo webrtc_ext.TrackInfo,
	remoteTrack *webrtc.TrackRemote,
	receiver *webrtc.RTPReceiver,
) {
	simulcast := webrtc_ext.RIDToSimulcastLayer(remoteTrack.RID())

	p.handleRemoteTrack(remoteTrack, trackInfo, simulcast, nil, func(packet *rtp.Packet) error {
		p.sink.Send(RTPPacketReceived{trackInfo, simulcast, packet})
		return nil
	})
}

func (p *Peer[ID]) handleNewAudioTrack(
	trackInfo webrtc_ext.TrackInfo,
	remoteTrack *webrtc.TrackRemote,
	receiver *webrtc.RTPReceiver,
) {
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

	p.handleRemoteTrack(remoteTrack, trackInfo, webrtc_ext.SimulcastLayerNone, localTrack, func(packet *rtp.Packet) error {
		if err = localTrack.WriteRTP(packet); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			return err
		}
		return nil
	})
}

func (p *Peer[ID]) handleRemoteTrack(
	remoteTrack *webrtc.TrackRemote,
	trackInfo webrtc_ext.TrackInfo,
	simulcast webrtc_ext.SimulcastLayer,
	outputTrack *webrtc.TrackLocalStaticRTP,
	handleRtpFn func(*rtp.Packet) error,
) {
	// Notify others that our track has just been published.
	p.state.AddRemoteTrack(remoteTrack)
	p.sink.Send(NewTrackPublished{trackInfo, simulcast, outputTrack})

	// Start a go-routine that reads the data from the remote track.
	go func() {
		// Call this when this goroutine ends.
		defer func() {
			p.state.RemoveRemoteTrack(remoteTrack)
			p.sink.Send(PublishedTrackFailed{trackInfo, simulcast})
		}()

		for {
			// Read the data from the remote track.
			packet, _, readErr := remoteTrack.ReadRTP()
			if readErr != nil {
				if readErr == io.EOF { // finished, no more data, no error, inform others
					p.logger.Info("remote track closed")
				} else { // finished, no more data, but with error, inform others
					p.logger.WithError(readErr).Error("failed to read from remote track")
				}
				return
			}

			// Handle the RTP packet.
			if err := handleRtpFn(packet); err != nil {
				p.logger.WithError(err).Error("failed to handle RTP packet")
				return
			}
		}
	}()
}
