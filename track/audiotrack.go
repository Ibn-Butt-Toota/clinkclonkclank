package track

import (
	"log/slog"
	"sync/atomic"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type (
	// all tracks are opus @ 48 khz
	TrackPacket struct {
		Layer        string
		Packet       *rtp.Packet
		TimeDiff     int64
		SequenceDiff int
		IsKeyframe   bool
	}

	AudioTrack struct {
		id         string
		Rid        string
		streamID   string
		kind       webrtc.RTPCodecType
		errorCount int

		TrackPacket     TrackPacket
		Priority        int
		PacketsReceived atomic.Uint64
		PacketsDropped  atomic.Uint64
		LastReceived    atomic.Value

		ssrc        webrtc.SSRC
		writeStream webrtc.TrackLocalWriter

		currentPayloadType uint8
	}

	AudioTrackState struct {
		Rid             string `json:"rid"`
		PacketsReceived uint64 `json:"packetsReceived"`
		PacketsDropped  uint64 `json:"packetsDropped"`
	}
)

func CreateAudioTrack(id string, rid string, streamID string, kind webrtc.RTPCodecType) *AudioTrack {
	return &AudioTrack{
		id:       id,
		Rid:      rid,
		streamID: streamID,
		kind:     kind,
	}
}

func (t *AudioTrack) WriteRTP(packet *rtp.Packet) error {
	packet.SSRC = uint32(t.ssrc)
	packet.PayloadType = t.currentPayloadType

	if _, err := t.writeStream.WriteRTP(&packet.Header, packet.Payload); err != nil {
		t.errorCount += 1

		if t.errorCount%50 == 0 {
			slog.Error("WHIPSession.AudioTrack.WriteRTP.Error", "errorCount", t.errorCount, "err", err)
			return err
		}
	}

	return nil
}
