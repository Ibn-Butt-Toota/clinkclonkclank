package session

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/FARTFARTFARTFARTFARTFARTFARTFARTFARTFRT/clinkclonkclank/track"
	"github.com/pion/webrtc/v4"
)

// Status for an individual streaming session
type whipSessionStatus struct {
	StreamKey   string    `json:"streamKey"`
	StreamStart time.Time `json:"streamStart"`
}

type WHEPSession struct {
	SessionID       string
	StreamKey       string
	IsSessionClosed atomic.Bool

	SessionClose sync.Once
	onClose      func(string)

	PeerConnectionLock sync.RWMutex
	PeerConnection     *webrtc.PeerConnection

	// Protects AudioTrack, AudioTimestamp, AudioPacketsWritten, AudioSequenceNumber
	AudioLock           sync.RWMutex
	AudioTrack          *track.AudioTrack
	AudioTimestamp      uint32
	AudioPacketsWritten uint64
	AudioSequenceNumber uint16
	AudioLayerCurrent   atomic.Value
}

// Create and start a new WHEP session
func CreateNewWHEP(
	whepSessionID string,
	streamKey string,
	audioTrack *track.AudioTrack,
	peerConnection *webrtc.PeerConnection,
) (w *WHEPSession) {
	slog.Info("WHEPSession.CreateNewWHEP", "whepSessionID", whepSessionID)

	w = &WHEPSession{
		SessionID:      whepSessionID,
		StreamKey:      streamKey,
		AudioTrack:     audioTrack,
		AudioTimestamp: 5000,
		PeerConnection: peerConnection,
	}

	w.AudioLayerCurrent.Store("")
	w.IsSessionClosed.Store(false)
	return w
}

// Sends provided audio packet to the WHEP session
func (w *WHEPSession) SendAudioPacket(packet track.TrackPacket) {
	if w.IsSessionClosed.Load() {
		return
	}

	w.AudioLock.Lock()
	if w.AudioTrack == nil {
		w.AudioLock.Unlock()
		return
	}

	w.AudioPacketsWritten += 1
	w.AudioTimestamp = uint32(int64(w.AudioTimestamp) + packet.TimeDiff)
	audioTrack := w.AudioTrack
	w.AudioLock.Unlock()

	if err := audioTrack.WriteRTP(packet.Packet); err != nil {
		if errors.Is(err, io.ErrClosedPipe) {
			slog.Info("WHEPSession.SendAudioPacket.ConnectionDropped")
			w.Close()
		} else {
			slog.Error("WHEPSession.SendAudioPacket.Error", "err", err)
		}
	}
}

// Closes down the WHEP session completely
func (w *WHEPSession) Close() {
	// Close WHEP channels
	w.SessionClose.Do(func() {
		slog.Info("WHEPSession.Close")
		w.IsSessionClosed.Store(true)

		// Close PeerConnection
		slog.Info("WHEPSession.Close.PeerConnection.GracefulClose")
		err := w.PeerConnection.Close()
		if err != nil {
			slog.Error("WHEPSession.Close.PeerConnection.Error", "err", err)
		}
		slog.Info("WHEPSession.Close.PeerConnection.GracefulClose.Completed")

		// Empty tracks
		w.AudioLock.Lock()
		w.AudioTrack = nil
		w.AudioLock.Unlock()

		if w.onClose != nil {
			w.onClose(w.SessionID)
		}
	})
}

func (w *WHEPSession) SetOnClose(onClose func(string)) {
	w.onClose = onClose
}

func (w *WHEPSession) RegisterWHEPHandlers(peerConnection *webrtc.PeerConnection) {
	slog.Info("WHEPSession.RegisterHandlers")

	peerConnection.OnICEConnectionStateChange(onWHEPICEConnectionStateChangeHandler(w))
}

func onWHEPICEConnectionStateChangeHandler(w *WHEPSession) func(webrtc.ICEConnectionState) {
	return func(state webrtc.ICEConnectionState) {
		slog.Info("WHEPSession.OnICEConnectionStateChange", "state", state)
		switch state {
		case
			webrtc.ICEConnectionStateConnected:
		case
			webrtc.ICEConnectionStateFailed,
			webrtc.ICEConnectionStateClosed:
			w.Close()
		default:
			slog.Info("WHEPSession.OnICEConnectionStateChange.Default", "state", state)
		}
	}
}

// Get the current status of the WHEP session
func (w *WHEPSession) GetWHEPSessionStatus() (state SessionState) {
	w.AudioLock.RLock()

	currentAudioLayer := w.AudioLayerCurrent.Load().(string)

	state = SessionState{
		ID: w.SessionID,

		AudioLayerCurrent:   currentAudioLayer,
		AudioTimestamp:      w.AudioTimestamp,
		AudioPacketsWritten: w.AudioPacketsWritten,
		AudioSequenceNumber: uint64(w.AudioSequenceNumber),
	}

	w.AudioLock.RUnlock()

	return
}

// Sets the requested audio layer for this WHEP session.
func (w *WHEPSession) SetAudioLayer(encodingID string) {
	w.AudioLayerCurrent.Store(encodingID)
}

// Reset per-publisher delivery state when a new WHIP publisher connects.
func (w *WHEPSession) ResetForNewPublisher() {
	w.AudioLayerCurrent.Store("")
}
