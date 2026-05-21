package session

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/FARTFARTFARTFARTFARTFARTFARTFARTFARTFRT/clinkclonkclank/track"
	"github.com/google/uuid"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type WHIPSession struct {
	ID                 string
	PeerConnection     *webrtc.PeerConnection
	closeOnce          sync.Once
	onClosed           func()
	PeerConnectionLock sync.RWMutex

	// Protects AudioTrack
	TracksLock  sync.RWMutex
	AudioTracks map[string]*track.AudioTrack

	// TODO: WHEPSessionsSnapshot should contain serializable state, not runtime references.
	WHEPSessionsSnapshot atomic.Value
}

func (w *WHIPSession) audioWriter(remoteTrack *webrtc.TrackRemote, streamKey string) {
	id := remoteTrack.RID()

	audioTrack, err := w.addAudioTrack(id, streamKey)
	if err != nil {
		slog.Error("AudioWriter.AddTrack.Error", "err", err)
		return
	}

	rtpPkt := &rtp.Packet{}
	rtpBuf := make([]byte, 1500)
	for {
		rtpRead, _, err := remoteTrack.Read(rtpBuf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				slog.Info("WHIPSession.AudioWriter.RtpPkt.EndOfStream")
				return
			} else {
				slog.Error("WHIPSession.AudioWriter.RtpPkt.Err", "err", err)
			}
		}

		audioTrack.PacketsReceived.Add(1)

		err = rtpPkt.Unmarshal(rtpBuf[:rtpRead])
		if err != nil {
			slog.Error("WHIPSession.AudioWriter.RtpPkt.Error", "err", err)
			continue
		}

		var sessions map[string]*WHEPSession
		if sessionsAny := w.WHEPSessionsSnapshot.Load(); sessionsAny != nil {
			sessions = sessionsAny.(map[string]*WHEPSession)
		}

		packet := track.TrackPacket{
			Layer:  id,
			Packet: rtpPkt,
		}

		for _, whepSession := range sessions {
			whepSession.SendAudioPacket(packet)
		}
	}
}

// Add a new AudioTrack to the WHIP session
func (w *WHIPSession) addAudioTrack(rid string, streamKey string) (*track.AudioTrack, error) {
	slog.Info("WHIPSession.AddAudioTrack", "streamKey", streamKey, "rid", rid)
	w.TracksLock.Lock()
	defer w.TracksLock.Unlock()

	if existingTrack, ok := w.AudioTracks[rid]; ok {
		return existingTrack, nil
	}

	track := track.CreateAudioTrack(
		"audio-"+uuid.New().String(),
		rid,
		streamKey,
		webrtc.RTPCodecTypeAudio,
	)

	track.LastReceived.Store(time.Time{})

	w.AudioTracks[track.Rid] = track

	return track, nil
}

// Remove Audio and Video tracks coming from the whip session id
func (w *WHIPSession) RemoveTracks() {
	slog.Info("WHIPSession.RemoveTracks")

	w.TracksLock.Lock()
	w.AudioTracks = make(map[string]*track.AudioTrack)
	w.TracksLock.Unlock()
}

func (w *WHIPSession) SetOnClosed(onClosed func()) {
	w.onClosed = onClosed
}

func (w *WHIPSession) notifyClosed() {
	w.closeOnce.Do(func() {
		if w.onClosed != nil {
			w.onClosed()
		}
	})
}

func (w *WHIPSession) registerWHIPHandlers(peerConnection *webrtc.PeerConnection, streamKey string) {
	slog.Info("WHIPSession.RegisterHandlers")

	// PeerConnection OnTrack handler
	w.PeerConnection.OnTrack(w.onTrackHandler(peerConnection, streamKey))

	// PeerConnection OnICEConnectionStateChange handler
	w.PeerConnection.OnICEConnectionStateChange(w.onICEConnectionStateChangeHandler())

	// PeerConnection OnConnectionStateChange
	w.PeerConnection.OnConnectionStateChange(w.onConnectionStateChange())
}

func (w *WHIPSession) onICEConnectionStateChangeHandler() func(webrtc.ICEConnectionState) {
	return func(state webrtc.ICEConnectionState) {
		if state == webrtc.ICEConnectionStateFailed || state == webrtc.ICEConnectionStateClosed {
			slog.Info("WHIPSession.PeerConnection.OnICEConnectionStateChange", "id", w.ID)
			w.notifyClosed()
		}
	}
}

func (w *WHIPSession) onTrackHandler(peerConnection *webrtc.PeerConnection, streamKey string) func(*webrtc.TrackRemote, *webrtc.RTPReceiver) {
	return func(remoteTrack *webrtc.TrackRemote, rtpReceiver *webrtc.RTPReceiver) {
		slog.Info("WHIPSession.PeerConnection.OnTrackHandler", "id", w.ID)

		if strings.HasPrefix(remoteTrack.Codec().MimeType, "audio") {
			// Handle audio stream
			w.audioWriter(remoteTrack, streamKey)
		}

		slog.Info("WHIPSession.OnTrackHandler.TrackStopped", "rid", remoteTrack.RID())
	}
}

func (w *WHIPSession) onConnectionStateChange() func(webrtc.PeerConnectionState) {
	return func(state webrtc.PeerConnectionState) {
		slog.Info("WHIPSession.PeerConnection.OnConnectionStateChange", "state", state)

		switch state {
		case webrtc.PeerConnectionStateClosed:
			w.notifyClosed()
		case webrtc.PeerConnectionStateFailed:
			slog.Info("WHIPSession.PeerConnection.OnConnectionStateChange: Host removed", "id", w.ID)
			w.notifyClosed()

		case webrtc.PeerConnectionStateConnected:
			slog.Info("WHIPSession.PeerConnection.OnConnectionStateChange: Host connected", "id", w.ID)

		}
	}
}

func (w *WHIPSession) AddPeerConnection(peerConnection *webrtc.PeerConnection, streamKey string) {
	slog.Info("WHIPSession.AddPeerConnection")

	w.PeerConnectionLock.Lock()
	existingPeerConnection := w.PeerConnection
	w.PeerConnection = peerConnection
	w.PeerConnectionLock.Unlock()

	if existingPeerConnection != nil && existingPeerConnection != peerConnection {
		slog.Info("WHIPSession.AddPeerConnection: Replacing existing peerconnection")
		if err := existingPeerConnection.GracefulClose(); err != nil {
			slog.Error("WHIPSession.AddPeerConnection.Close.Error", "err", err)
		}
	}

	w.registerWHIPHandlers(peerConnection, streamKey)
}

func (w *WHIPSession) RemovePeerConnection() {
	slog.Info("WHIPSession.RemovePeerConnection", "id", w.ID)

	w.PeerConnectionLock.Lock()
	peerConnection := w.PeerConnection
	w.PeerConnection = nil
	w.PeerConnectionLock.Unlock()

	if peerConnection == nil {
		return
	}

	if err := peerConnection.Close(); err != nil {
		slog.Error("WHIPSession.RemovePeerConnection.Error", "err", err)
	}

	slog.Info("WHIPSession.RemovePeerConnection.Completed", "id", w.ID)
}

// Returns all available Audio layers of the provided stream key
// func (w *WHIPSession) GetAvailableLayersEvent() string {
// 	audioLayers := []simulcastLayerResponse{}

// 	w.TracksLock.RLock()

// 	// Add available audio layers
// 	for track := range w.AudioTracks {
// 		audioLayers = append(audioLayers, simulcastLayerResponse{
// 			EncodingID: w.AudioTracks[track].Rid,
// 		})
// 	}

// 	w.TracksLock.RUnlock()

// 	resp := map[string]map[string][]simulcastLayerResponse{
// 		"1": {
// 			"layers": videoLayers,
// 		},
// 		"2": {
// 			"layers": audioLayers,
// 		},
// 	}

// 	jsonResult, err := json.Marshal(resp)
// 	if err != nil {
// 		slog.Error("Error converting response to Json", "resp", resp, "err", err)
// 	}

// 	return "event: layers\ndata: " + string(jsonResult) + "\n\n"
// }
