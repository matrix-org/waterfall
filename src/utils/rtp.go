/*
Copyright 2022 The Matrix.org Foundation C.I.C.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

//nolint
package utils

func IsRTPPacketKeyFrame(mimeType string, payload []byte) bool {
	switch mimeType {
	case "video/VP8":
		return Unmarshal(payload)
	case "video/h264":
		return isH264Keyframe(payload)
	}

	return false
}

// isH264Keyframe detects if h264 payload is a keyframe
// this code was taken from https://github.com/jech/galene/blob/codecs/rtpconn/rtpreader.go#L45
// all credits belongs to Juliusz Chroboczek @jech and the awesome Galene SFU.
func isH264Keyframe(payload []byte) bool {
	if len(payload) < 1 {
		return false
	}
	nalu := payload[0] & 0x1F
	if nalu == 0 {
		// reserved
		return false
	} else if nalu <= 23 {
		// simple NALU
		return nalu == 5
	} else if nalu == 24 || nalu == 25 || nalu == 26 || nalu == 27 {
		// STAP-A, STAP-B, MTAP16 or MTAP24
		i := 1
		if nalu == 25 || nalu == 26 || nalu == 27 {
			// skip DON
			i += 2
		}
		for i < len(payload) {
			if i+2 > len(payload) {
				return false
			}
			length := uint16(payload[i])<<8 |
				uint16(payload[i+1])
			i += 2
			if i+int(length) > len(payload) {
				return false
			}
			offset := 0
			if nalu == 26 {
				offset = 3
			} else if nalu == 27 {
				offset = 4
			}
			if offset >= int(length) {
				return false
			}
			n := payload[i+offset] & 0x1F
			if n == 7 {
				return true
			} else if n >= 24 {
				// is this legal?
				//Logger.V(0).Info("Non-simple NALU within a STAP")
			}
			i += int(length)
		}
		if i == len(payload) {
			return false
		}
		return false
	} else if nalu == 28 || nalu == 29 {
		// FU-A or FU-B
		if len(payload) < 2 {
			return false
		}
		if (payload[1] & 0x80) == 0 {
			// not a starting fragment
			return false
		}
		return payload[1]&0x1F == 7
	}
	return false
}

// Unmarshal parses the passed byte slice and stores the result in the VP8 this method is called upon
func Unmarshal(payload []byte) bool {
	if payload == nil {
		return false
	}

	payloadLen := len(payload)

	if payloadLen < 1 {
		return false
	}

	idx := 0
	S := payload[idx]&0x10 > 0
	// Check for extended bit control
	if payload[idx]&0x80 > 0 {
		idx++
		if payloadLen < idx+1 {
			return false
		}
		// Check if T is present, if not, no temporal layer is available
		TemporalSupported := payload[idx]&0x20 > 0
		K := payload[idx]&0x10 > 0
		L := payload[idx]&0x40 > 0
		// Check for PictureID
		if payload[idx]&0x80 > 0 {
			idx++
			if payloadLen < idx+1 {
				return false
			}
			// Check if m is 1, then Picture ID is 15 bits
			if payload[idx]&0x80 > 0 {
				idx++
				if payloadLen < idx+1 {
					return false
				}
			}
		}
		// Check if TL0PICIDX is present
		if L {
			idx++
			if payloadLen < idx+1 {
				return false
			}

			if int(idx) >= payloadLen {
				return false
			}
		}
		if TemporalSupported || K {
			idx++
			if payloadLen < idx+1 {
				return false
			}
		}
		if idx >= payloadLen {
			return false
		}
		idx++
		if payloadLen < idx+1 {
			return false
		}
		// Check is packet is a keyframe by looking at P bit in vp8 payload
		return payload[idx]&0x01 == 0 && S
	} else {
		idx++
		if payloadLen < idx+1 {
			return false
		}
		// Check is packet is a keyframe by looking at P bit in vp8 payload
		return payload[idx]&0x01 == 0 && S
	}
}
