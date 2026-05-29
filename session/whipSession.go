package session

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/FARTFARTFARTFARTFARTFARTFARTFARTFARTFRT/clinkclonkclank/track"
	"github.com/pion/opus"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media/samplebuilder"
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

func (w *WHIPSession) audioWriter(remoteTrack *webrtc.TrackRemote, streamID string) {
	id := remoteTrack.RID()

	audioTrack, err := w.addAudioTrack(id, streamID)
	if err != nil {
		slog.Error("AudioWriter.AddTrack.Error", "err", err)
		return
	}

	// sample interval * khz * channels
	pcm := make([]byte, 20*16*2)

	sb := samplebuilder.New(10, &codecs.OpusPacket{}, 48000)
	opusToPCM, err := opus.NewDecoderWithOutput(16000, 1)
	if err != nil {
		return
	}

	// buffer the packets in the channel because we need to read as fast as we can.
	packets := make(chan *rtp.Packet, 100)

	// by default, listen and parse. <- enhance via audio cue
	// upon keyword, stop listening/transcribing.
	// after output / signal, resume listening/transcribing. <- enhance via using audio cues

	// decent, but slow. ~1s delay. supports streaming. doesn't flush the buffer with a timer though.
	// build qwen_asr by following the steps here: https://github.com/antirez/qwen-asr
	// asr := exec.Command("./qwen_asr", "-d", "qwen3-asr-1.7b", "--stdin", "--stream", "--silent", "--enc-window-sec", "2", "--stream-max-new-tokens", "48")

	// parses faster, but only once the stdin pipe is completed.
	// `brew install cargo`
	// `cargo install qwen-asr-cli` (atm built from my fork)

	// custom configured to pause when not running, waits for `syscall.SIGUSR1` to resume.
	asr := exec.Command(
		"./qwen-asr",
		"-d",
		"qwen3-asr-0.6b",
		"--stdin",
		"--stream",
		"--silent",
		"--stream-chunk-sec",
		"1",
		// "--debug",
	)

	pcmToASR, err := asr.StdinPipe()
	if err != nil {
		slog.Error("Failed to create stdin pipe for ASR", "err", err)
		return
	}

	asr.Stderr = os.Stdout

	startedASR := false
	var recording bytes.Buffer

	// write to the recording and the ASR command's stdin
	stdinMW := io.MultiWriter(pcmToASR, &recording)

	var outBuf asrOutBuffer
	sawKeyword := make(chan struct{}, 1)

	keywordWatcher := &KeywordWatcher{
		onKeyword: func() {
			select {
			case sawKeyword <- struct{}{}:
			default:
			}
		},
	}

	asr.Stdout = io.MultiWriter(&outBuf, keywordWatcher)

	slog.Info("Beginning to read RTP from the user")
	asrStage := true

	// at the end we need to clean up the command's stdin
	defer func() {
		if closeErr := pcmToASR.Close(); closeErr != nil {
			slog.Error("Error while closing ASR stdin")
		}

		// at this point the stream must've stopped, so write the file to memory
		file, err := os.Create(streamID + ".pcm")
		if err != nil {
			return
		}

		slog.Info("Saving recording: ", "file", file.Name())
		if _, err := file.Write(recording.Bytes()); err != nil {
			slog.Error("WHIPSession.audioWriter.writeRecording", "err", err)
		}
	}()

	// pocketwatch
	go func() {
		select {
		case <-sawKeyword:
			asrStage = false
		}
	}()

	go func() {
		for {
			pkt, _, readErr := remoteTrack.ReadRTP()
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					slog.Info("WHIPSession.AudioWriter.RtpPkt.EndOfStream")
					packets <- nil

					return
				}

				slog.Error("WHIPSession.AudioWriter error while reading rtp packets", "err", readErr)
			}

			// keep receiving and enqueueing packets while we're still in the asr stage
			if asrStage {
				audioTrack.PacketsReceived.Add(1)
				packets <- pkt
			} else {
				packets <- nil
			}
		}
	}()

	slog.Info("Decoding Opus to PCM")

outer:
	for {
		select {
		// this will consume packets as long as we're in the asrStage.
		// if the asrStage is false OR we received EndOfStream, we will exit.
		case pkt := <-packets:
			if pkt == nil {
				break outer
			}

			var sessions map[string]*WHEPSession
			if sessionsAny := w.WHEPSessionsSnapshot.Load(); sessionsAny != nil {
				sessions = sessionsAny.(map[string]*WHEPSession)
			}

			packet := track.TrackPacket{
				Layer:  id,
				Packet: pkt,
			}

			for _, whepSession := range sessions {
				whepSession.SendAudioPacket(packet)
			}

			sb.Push(pkt)

			// decode each sample to PCM.
			for {
				sample := sb.Pop()
				if sample == nil {
					break
				}

				// sample.Data contains the raw opus data
				if _, _, err := opusToPCM.Decode(sample.Data, pcm); err != nil {
					slog.Error("Error while decoding raw opus to PCM", "err", err)
				}

				if _, err := stdinMW.Write(pcm); err != nil {
					slog.Error("Error while writing pcm to ASR.stdin or recording buffer")
				}
			}

			if !startedASR {
				go func() {
					if err := asr.Run(); err != nil {
						slog.Error("ASR failed to start", "err", err)
						return
					}
				}()

				slog.Info("Starting ASR")
				startedASR = true
			}
		}
	}

	slog.Info("escaped pkt loop")

	// if we get here because asrStage is over, send this text to the second model.
	if !asrStage {
		slog.Info("Saving transcript to txt")

		// for now we'll send it to the third model.
		// write the transcript then call the python file.
		transcript, err := os.Create("transcript.txt")
		if err != nil {
			slog.Error("error while generating transcript", "err", err)
			return
		}

		if _, err := transcript.Write(outBuf.buf.Bytes()); err != nil {
			slog.Error("error unable to write the transcript to the file", "transcript", keywordWatcher.tail, "err", err)
			return
		}

		// now lets run the python script that reads from the transcript.
		slog.Info("Generating TTS")
		kitten := exec.Command("python", "tts.py")

		// do a blocking run of the tts.
		if err = kitten.Run(); err != nil {
			slog.Error("Err when running kitten", "err", err)
		}
		slog.Info("TTS created!")
	}

	slog.Info("Stream done")
}

// Add a new AudioTrack to the WHIP session
func (w *WHIPSession) addAudioTrack(rid string, streamID string) (*track.AudioTrack, error) {
	slog.Info("WHIPSession.AddAudioTrack", "uid", streamID, "rid", rid)
	w.TracksLock.Lock()
	defer w.TracksLock.Unlock()

	if existingTrack, ok := w.AudioTracks[rid]; ok {
		return existingTrack, nil
	}

	track := track.CreateAudioTrack(
		"audio-"+string(streamID),
		rid,
		streamID,
		webrtc.RTPCodecTypeAudio,
	)

	track.LastReceived.Store(time.Time{})

	w.AudioTracks[track.RID()] = track

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

func (w *WHIPSession) registerWHIPHandlers(peerConnection *webrtc.PeerConnection, streamID string) {
	slog.Info("WHIPSession.RegisterHandlers")

	// PeerConnection OnTrack handler
	w.PeerConnection.OnTrack(w.onTrackHandler(peerConnection, streamID))

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

func (w *WHIPSession) onTrackHandler(peerConnection *webrtc.PeerConnection, streamID string) func(*webrtc.TrackRemote, *webrtc.RTPReceiver) {
	return func(remoteTrack *webrtc.TrackRemote, rtpReceiver *webrtc.RTPReceiver) {
		slog.Info("WHIPSession.PeerConnection.OnTrackHandler", "id", w.ID)

		if strings.HasPrefix(remoteTrack.Codec().MimeType, "audio") {
			// Handle audio stream
			w.audioWriter(remoteTrack, streamID)
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

func (w *WHIPSession) AddPeerConnection(pc *webrtc.PeerConnection, streamID string) {
	slog.Info("WHIPSession.AddPeerConnection")

	w.PeerConnectionLock.Lock()
	currPC := w.PeerConnection
	w.PeerConnection = pc
	w.PeerConnectionLock.Unlock()

	if currPC != nil && currPC != pc {
		slog.Info("WHIPSession.AddPeerConnection: Replacing existing peerconnection")
		if err := currPC.GracefulClose(); err != nil {
			slog.Error("WHIPSession.AddPeerConnection.Close.Error", "err", err)
		}
	}

	w.registerWHIPHandlers(pc, streamID)
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
