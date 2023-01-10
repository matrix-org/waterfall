package peer

import (
	"errors"
	"io"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/pion/webrtc/v3"
)

func (p *Peer[ID]) handleNewVideoTrack(
	trackInfo common.TrackInfo,
	remoteTrack *webrtc.TrackRemote,
	receiver *webrtc.RTPReceiver,
) {
	simulcast := common.RIDToSimulcastLayer(remoteTrack.RID())

	// Notify others that our track has just been published.
	p.state.AddRemoteTrack(remoteTrack)
	p.sink.Send(NewTrackPublished{trackInfo, simulcast, nil})

	// Start forwarding the data from the remote track to the local track,
	// so that everyone who is subscribed to this track will receive the data.
	go func() {
		for {
			packet, _, readErr := remoteTrack.ReadRTP()
			if readErr != nil {
				if readErr == io.EOF { // finished, no more data, no error, inform others
					p.logger.Info("remote track closed")
				} else { // finished, no more data, but with error, inform others
					p.logger.WithError(readErr).Error("failed to read from remote track")
				}
				p.state.RemoveRemoteTrack(remoteTrack)
				p.sink.Send(PublishedTrackFailed{trackInfo, simulcast})
				return
			}

			p.sink.Send(RTPPacketReceived{trackInfo, simulcast, packet})
		}
	}()
}

func (p *Peer[ID]) handleNewAudioTrack(
	trackInfo common.TrackInfo,
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

	p.state.AddRemoteTrack(remoteTrack)
	p.sink.Send(NewTrackPublished{trackInfo, common.SimulcastLayerNone, localTrack})

	// Start forwarding the data from the remote track to the local track,
	// so that everyone who is subscribed to this track will receive the data.
	go func() {
		rtpBuf := make([]byte, 1400)

		// Call this when this goroutine ends.
		defer func() {
			p.sink.Send(PublishedTrackFailed{trackInfo, common.SimulcastLayerNone})
			p.state.RemoveRemoteTrack(remoteTrack)
		}()

		for {
			index, _, readErr := remoteTrack.Read(rtpBuf)
			if readErr != nil {
				if readErr == io.EOF { // finished, no more data, no error, inform others
					p.logger.Info("remote track closed")
				} else { // finished, no more data, but with error, inform others
					p.logger.WithError(readErr).Error("failed to read from remote track")
				}
				return
			}

			// ErrClosedPipe means we don't have any subscribers, this is ok if no peers have connected yet.
			if _, err = localTrack.Write(rtpBuf[:index]); err != nil && !errors.Is(err, io.ErrClosedPipe) {
				p.logger.WithError(err).Error("failed to write to local track")
				return
			}
		}
	}()
}
