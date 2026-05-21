package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/FARTFARTFARTFARTFARTFARTFARTFARTFARTFRT/clinkclonkclank/manager"
	"github.com/pion/sdp/v3"
	w "github.com/pion/webrtc/v4"
)

const (
	TCPPORT = 5004
	UDPPORT = 5005
)

func signalCandidate(addr string, candidate *w.ICECandidate) error {
	payload := []byte(candidate.ToJSON().Candidate)
	resp, err := http.Post(
		fmt.Sprintf("http://%s/candidate", addr),
		"application/json; charset=utf-8",
		bytes.NewReader(payload),
	)
	if err != nil {
		return err
	}

	return resp.Body.Close()
}

func main() {
	se := w.SettingEngine{}

	// use ice-lite since i don't need NAT traversal
	se.SetLite(true)
	se.SetNetworkTypes([]w.NetworkType{w.NetworkTypeTCP4, w.NetworkTypeUDP4})

	// setting tcp and udp muxes
	tcpListener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: TCPPORT})
	se.SetICETCPMux(w.NewICETCPMux(nil, tcpListener, 8))

	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: UDPPORT})
	se.SetICEUDPMux(w.NewICEUDPMux(nil, udpListener))

	api := w.NewAPI(w.WithSettingEngine(se))

	// create the peerConnection
	peerConnection, err := api.NewPeerConnection(w.Configuration{})
	if err != nil {
		panic(err)
	}

	defer func() {
		if closeErr := peerConnection.Close(); closeErr != nil {
			fmt.Printf("cannot close peerConnection: %v\n", closeErr)
		}
	}()

	// whip for ingest
	http.HandleFunc("/whip", func(res http.ResponseWriter, req *http.Request) {
		// userID is unused here...
		userID, ok := checkReqAuth(res, req)
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

		whipAnswer, sessionID, err := whip(string(offer))
		if err != nil {
			res.WriteHeader(http.StatusInternalServerError)
			res.Write([]byte("Could not begin whip"))
			return
		}

		res.Header().Add("Content-Type", "application/sdp")
		res.WriteHeader(http.StatusCreated)
		// idk
		res.Write([]byte(whipAnswer + "," + sessionID))

		return
	})

	// whep for playback
	http.HandleFunc("/whep", func(res http.ResponseWriter, req *http.Request) {
		print("tup")
	})

	// Set the handler for Peer connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnConnectionStateChange(func(state w.PeerConnectionState) {
		fmt.Printf("Peer Connection State has changed: %s\n", state.String())

		if state == w.PeerConnectionStateFailed {
			// Wait until PeerConnection has had no network activity for 30 seconds or another failure.
			// It may be reconnected using an ICE Restart.
			// Use w.PeerConnectionStateDisconnected if you are interested in detecting faster timeout.
			// Note that the PeerConnection may come back from PeerConnectionStateDisconnected.
			fmt.Println("Peer Connection has gone to failed exiting")
			os.Exit(0)
		}

		if state == w.PeerConnectionStateClosed {
			// PeerConnection was explicitly closed. This usually happens from a DTLS CloseNotify
			fmt.Println("Peer Connection has gone to closed exiting")
			os.Exit(0)
		}
	})

	// Start HTTP server with whip / whep endpoints
	// nolint: gosec
	go func() { panic(http.ListenAndServe(*offerAddr, nil)) }()

	// Create an offer to send to the other process
	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		panic(err)
	}

	// Sets the LocalDescription, and starts our UDP listeners
	// Note: this will start the gathering of ICE candidates
	if err = peerConnection.SetLocalDescription(offer); err != nil {
		panic(err)
	}

	// Send our offer to the HTTP server listening in the other process
	payload, err := json.Marshal(offer)
	if err != nil {
		panic(err)
	}
	resp, err := http.Post( // nolint:noctx
		fmt.Sprintf("http://%s/sdp", *answerAddr),
		"application/json; charset=utf-8",
		bytes.NewReader(payload),
	)
	if err != nil {
		panic(err)
	} else if err := resp.Body.Close(); err != nil {
		panic(err)
	}

	// Block forever
	select {}
}

func checkReqAuth(res http.ResponseWriter, req *http.Request) (userID int, ok bool) {
	tok := req.Header.Get("Authorization")
	if !strings.HasPrefix(tok, "Bearer ") {
		res.WriteHeader(http.StatusUnauthorized)
		res.Write([]byte("Unauthorized"))
		return -1, false
	}

	// later make this userID be uuid7 or something idk figure it out later (this will be the user session ID)
	return 1, true
}

// Initialize WHIP session for incoming stream
func whip(offer string) (parsedSDP string, sessionID string, err error) {
	var parsed sdp.SessionDescription
	if err := parsed.Unmarshal([]byte(offer)); err != nil {
		http.Get("asdf.com")
		return "", "", fmt.Errorf("Bad Response", http.StatusBadRequest)
	}

	session, err := manager.SessionsManager.GetOrAddSession(true)
	if err != nil {
		return "", "", err
	}

	peerConnection, err := w.NewPeerConnection(w.Configuration{})
	if err != nil || peerConnection == nil {
		if peerConnection != nil {
			if closeErr := peerConnection.Close(); closeErr != nil {
				fmt.Printf("WHIP NewPeerConnection Close Failed %v", closeErr)
			}
		}
		return "", "", err
	}

	if err := session.AddHost(peerConnection); err != nil {
		return "", "", err
	}

	host := session.Host.Load()
	if host == nil {
		return "", "", errors.New("host session not available")
	}

	// add dtx (opus discontinuous transmission) to the sdp to prevent sending audio packets if it's silent
	if !strings.Contains(parsedSDP, ";usedtx=1") {
		parsedSDP += ";usedtx=1"
	}
	return parsedSDP, host.ID, nil
}

func setupDataChannel(dataChannel *w.DataChannel) {
	// Register channel opening handling
	dataChannel.OnOpen(func() {
		fmt.Printf(
			"Chat started.",
			dataChannel.Label(), dataChannel.ID(),
		)
	})

	// Register text message handling
	dataChannel.OnMessage(func(msg w.DataChannelMessage) {
		fmt.Printf("Message from DataChannel '%s': '%s'\n", dataChannel.Label(), string(msg.Data))
	})
}
