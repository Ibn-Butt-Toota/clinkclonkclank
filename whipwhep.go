package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	s "github.com/FARTFARTFARTFARTFARTFARTFARTFARTFARTFRT/clinkclonkclank/session"
	"github.com/FARTFARTFARTFARTFARTFARTFARTFARTFARTFRT/clinkclonkclank/track"
	"github.com/google/uuid"
	w "github.com/pion/webrtc/v4"
)

var (
	errBadResponse = errors.New("bad response")
)

func corsHandler(next func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(res http.ResponseWriter, req *http.Request) {
		res.Header().Set("Access-Control-Allow-Origin", "*")
		res.Header().Set("Access-Control-Allow-Methods", "*")
		res.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Session-ID")
		res.Header().Set("Access-Control-Expose-Headers", "*")

		if req.Method != http.MethodOptions {
			next(res, req)
		}
	}
}

func whipHandler(res http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		res.WriteHeader(http.StatusMethodNotAllowed)
		res.Write([]byte("Method not allowed"))
		return
	}

	_, ok := checkReqAuth(res, req)
	if !ok {
		return
	}

	// read the SDP request
	offer, err := io.ReadAll(req.Body)
	if err != nil || string(offer) == "" {
		res.WriteHeader(http.StatusBadRequest)
		res.Write([]byte("Error reading offer"))
		return
	}

	// check if the header already includes a Session-ID
	sessionID := req.Header.Get("Session-ID")

	whipAnswer, sessionID, err := whip(string(offer), sessionID)
	if err != nil {
		slog.Error("whip failed", "err", err)
		res.WriteHeader(http.StatusInternalServerError)
		res.Write([]byte("Could not begin whip"))
		return
	}

	res.Header().Add("Content-Type", "application/sdp")
	// manage session via header
	res.Header().Add("Session-ID", sessionID)
	res.WriteHeader(http.StatusCreated)

	res.Write([]byte(whipAnswer))

	return
}

// Initialize WHIP session for incoming stream
func whip(offer string, streamID string) (parsedSDP string, sessionID string, err error) {
	session, err := s.SessionsManager.GetOrAddSession(streamID)
	if err != nil {
		return "", "", err
	}

	pc, err := API.NewPeerConnection(w.Configuration{})
	if err != nil || pc == nil {
		if pc != nil {
			if closeErr := pc.Close(); closeErr != nil {
				fmt.Printf("WHIP NewPeerConnection Close Failed %v", closeErr)
			}
		}
		return "", "", err
	}

	// add dtx (opus discontinuous transmission) to the sdp to prevent sending audio packets if it's silent
	// TODO: also add htis to audio fmt lines
	// if !strings.Contains(parsedSDP, ";usedtx=1") {
	// 	parsedSDP += ";usedtx=1"
	// }

	// Setup PeerConnection RemoteDescription
	sessionDescription := w.SessionDescription{
		SDP:  string(offer),
		Type: w.SDPTypeOffer,
	}

	if err := pc.SetRemoteDescription(sessionDescription); err != nil {
		return "", "", err
	}

	gatheringComplete := w.GatheringCompletePromise(pc)

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return "", "", err
	}

	if err := pc.SetLocalDescription(answer); err != nil {
		return "", "", err
	}

	if err := session.AddHost(pc); err != nil {
		return "", "", err
	}

	host := session.Host.Load()
	if host == nil {
		return "", "", errors.New("host session not available")
	}

	// await gathering ice
	<-gatheringComplete

	return pc.LocalDescription().SDP, host.ID, nil
}

func whepHandler(res http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		res.WriteHeader(http.StatusMethodNotAllowed)
		res.Write([]byte("Method not allowed"))
		return
	}

	tok, ok := checkReqAuth(res, req)
	if !ok {
		return
	}

	// read the SDP request
	offer, err := io.ReadAll(req.Body)
	if err != nil || string(offer) == "" {
		res.WriteHeader(http.StatusBadRequest)
		res.Write([]byte("Error reading offer"))
		return
	}

	// check if the header already includes a Session-ID
	sessionID := req.Header.Get("Session-ID")

	whipAnswer, sessionID, err := whep(string(offer), sessionID, tok)
	if err != nil {
		slog.Error("whep failed", "err", err)
		res.WriteHeader(http.StatusInternalServerError)
		res.Write([]byte("Could not begin whep"))
		return
	}

	res.Header().Add("Content-Type", "application/sdp")
	// manage session via header
	res.Header().Add("Session-ID", sessionID)
	res.WriteHeader(http.StatusCreated)

	res.Write([]byte(whipAnswer))

	return
}

// Initialize WHIP session for incoming stream
func whep(offer string, streamID string, token string) (parsedSDP string, sessionID string, err error) {
	session, err := s.SessionsManager.GetOrAddSession(streamID)
	if err != nil {
		return "", "", err
	}

	whepSessionID := uuid.New().String()

	pc, err := API.NewPeerConnection(w.Configuration{})
	if err != nil || pc == nil {
		if pc != nil {
			if closeErr := pc.Close(); closeErr != nil {
				fmt.Printf("WHEP NewPeerConnection Close Failed %v", closeErr)
			}
		}
		return "", "", err
	}

	pc.OnICEConnectionStateChange(func(state w.ICEConnectionState) {
		if state == w.ICEConnectionStateFailed || state == w.ICEConnectionStateClosed {
			if err := pc.Close(); err != nil {
				slog.Warn("Error closing peer connection when ice is %v with error: %v", state, err)
			}
		}
	})

	audioTrack := track.CreateAudioTrack("audio", "pion", "TODO", w.RTPCodecTypeAudio)

	audioRTCPSender, err := pc.AddTrack(audioTrack)
	if err != nil {
		return "", "", err
	}

	// Setup PeerConnection RemoteDescription
	sessionDescription := w.SessionDescription{
		SDP:  string(offer),
		Type: w.SDPTypeOffer,
	}

	if err := pc.SetRemoteDescription(sessionDescription); err != nil {
		return "", "", err
	}

	gatheringComplete := w.GatheringCompletePromise(pc)

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return "", "", err
	}

	if err := pc.SetLocalDescription(answer); err != nil {
		return "", "", err
	}

	if err := session.AddWHEP(whepSessionID, pc, audioTrack, audioRTCPSender); err != nil {
		return "", "", err
	}

	// await gathering ice
	<-gatheringComplete

	return pc.LocalDescription().SDP, whepSessionID, nil
}
