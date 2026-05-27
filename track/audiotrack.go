package track

import (
	"log/slog"
	"sync/atomic"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	w "github.com/pion/webrtc/v4"
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
		rid        string
		streamID   string
		kind       w.RTPCodecType
		errorCount int

		TrackPacket     TrackPacket
		Priority        int
		PacketsReceived atomic.Uint64
		PacketsDropped  atomic.Uint64
		LastReceived    atomic.Value

		ssrc        w.SSRC
		writeStream w.TrackLocalWriter

		payloadTypeOpus    uint8
		currentPayloadType uint8
	}

	AudioTrackState struct {
		Rid             string `json:"rid"`
		PacketsReceived uint64 `json:"packetsReceived"`
		PacketsDropped  uint64 `json:"packetsDropped"`
	}
)

func CreateAudioTrack(id string, rid string, streamID string, kind w.RTPCodecType) *AudioTrack {
	return &AudioTrack{
		id:       id,
		rid:      rid,
		streamID: streamID,
		kind:     kind,
	}
}

func (t *AudioTrack) ID() string                { return t.id }
func (t *AudioTrack) RID() string               { return t.rid }
func (t *AudioTrack) StreamID() string          { return t.streamID }
func (t *AudioTrack) Kind() webrtc.RTPCodecType { return t.kind }

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

func (t *AudioTrack) Bind(ctx w.TrackLocalContext) (w.RTPCodecParameters, error) {
	t.ssrc = ctx.SSRC()
	t.writeStream = ctx.WriteStream()

	codecParameters := ctx.CodecParameters()
	for parameters := range codecParameters {
		t.payloadTypeOpus = uint8(codecParameters[parameters].PayloadType)
		t.currentPayloadType = t.payloadTypeOpus
		slog.Info("WHIPSession.TrackMultiCodec: Binding AudioTrack Type", "uid", t.streamID, "payloadType", t.currentPayloadType)

		t.kind = w.RTPCodecTypeAudio
		return w.RTPCodecParameters{
			PayloadType: codecParameters[parameters].PayloadType,
			RTPCodecCapability: w.RTPCodecCapability{
				MimeType:     codecParameters[parameters].MimeType,
				RTCPFeedback: codecParameters[parameters].RTCPFeedback,
				ClockRate:    codecParameters[parameters].ClockRate,
				SDPFmtpLine:  codecParameters[parameters].SDPFmtpLine,
			},
		}, nil
	}

	// return a hardcoded codec because this is the only codec that should be bound
	return w.RTPCodecParameters{
		RTPCodecCapability: w.RTPCodecCapability{
			MimeType:     w.MimeTypeOpus,
			ClockRate:    48000,
			Channels:     2,
			SDPFmtpLine:  "",
			RTCPFeedback: nil,
		},
		PayloadType: 111,
	}, nil
}

func (t *AudioTrack) Unbind(context w.TrackLocalContext) error {
	return nil
}
