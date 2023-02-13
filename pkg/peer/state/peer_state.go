package state

import (
	"sync"

	"github.com/pion/webrtc/v3"
)

type PeerState struct {
	mutex       sync.Mutex
	dataChannel *webrtc.DataChannel
}

func NewPeerState() *PeerState {
	return &PeerState{}
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
