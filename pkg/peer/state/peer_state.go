package state

import (
	"sync"

	"github.com/matrix-org/waterfall/pkg/common"
	"github.com/pion/webrtc/v3"
)

type RemoteTrackId struct {
	id        string
	simulcast common.SimulcastLayer
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

	p.remoteTracks[RemoteTrackId{track.ID(), common.RIDToSimulcastLayer(track.RID())}] = track
}

func (p *PeerState) RemoveRemoteTrack(track *webrtc.TrackRemote) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	delete(p.remoteTracks, RemoteTrackId{track.ID(), common.RIDToSimulcastLayer(track.RID())})
}

func (p *PeerState) GetRemoteTrack(id string, simulcast common.SimulcastLayer) *webrtc.TrackRemote {
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
