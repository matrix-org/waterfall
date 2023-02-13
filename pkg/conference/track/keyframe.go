package track

import (
	"fmt"

	"github.com/matrix-org/waterfall/pkg/conference/publisher"
	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/pion/webrtc/v3"
)

func (p *PublishedTrack[SubscriberID]) handleKeyFrameRequest(simulcast webrtc_ext.SimulcastLayer) error {
	publisher := p.getPublisher(simulcast)
	if publisher == nil {
		return fmt.Errorf("publisher with simulcast %s not found", simulcast)
	}

	track, err := extractRemoteTrack(publisher)
	if err != nil {
		return err
	}

	return p.owner.requestKeyFrame(track)
}

func (p *PublishedTrack[SubscriberID]) getPublisher(simulcast webrtc_ext.SimulcastLayer) *publisher.Publisher {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// Get the track that we need to request a key frame for.
	return p.video.publishers[simulcast]
}

func extractRemoteTrack(pub *publisher.Publisher) (*webrtc.TrackRemote, error) {
	// Get the track that we need to request a key frame for.
	track := pub.GetTrack()
	remoteTrack, ok := track.(*publisher.RemoteTrack)
	if !ok {
		return nil, fmt.Errorf("not a remote track in publisher")
	}

	return remoteTrack.Track, nil
}
