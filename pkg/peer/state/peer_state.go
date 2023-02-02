package state

import (
	"sync"

	"github.com/matrix-org/waterfall/pkg/webrtc_ext"
	"github.com/pion/webrtc/v3"
)

type RemoteTrackId struct {
	id        string
	simulcast webrtc_ext.SimulcastLayer
}

type PeerState struct {
	mutex        sync.Mutex
	dataChannel  *webrtc.DataChannel
	remoteTracks map[RemoteTrackId]*webrtc.TrackRemote
}

func NewPeerState() *PeerState {
	return &PeerState{
		remoteTracks: make(map[RemoteTrackId]*webrtc.TrackRemote),
	}
}

func (p *PeerState) AddRemoteTrack(track *webrtc.TrackRemote) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.remoteTracks[RemoteTrackId{track.ID(), webrtc_ext.RIDToSimulcastLayer(track.RID())}] = track
}

func (p *PeerState) RemoveRemoteTrack(track *webrtc.TrackRemote) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	delete(p.remoteTracks, RemoteTrackId{track.ID(), webrtc_ext.RIDToSimulcastLayer(track.RID())})
}

func (p *PeerState) GetRemoteTrack(id string, simulcast webrtc_ext.SimulcastLayer) *webrtc.TrackRemote {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	return p.remoteTracks[RemoteTrackId{id, simulcast}]
}

func (p *PeerState) SetDataChannel(dc *webrtc.DataChannel) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.dataChannel = dc
}

func (p *PeerState) GetDataChannel() *webrtc.DataChannel {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	return p.dataChannel
}
