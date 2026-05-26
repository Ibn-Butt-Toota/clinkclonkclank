package main

import (
	"bytes"
	"embed"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"

	s "github.com/FARTFARTFARTFARTFARTFARTFARTFARTFARTFRT/clinkclonkclank/session"
	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	w "github.com/pion/webrtc/v4"
)

//go:embed assets
var assets embed.FS

const (
	TCPPORT      int = 5004
	UDPPORT      int = 5005
	TCP_MUX_ADDR     = "127.0.0.1"
	HTTP_ADDRESS     = ":8080"
)

var (
	API   *w.API
	CODEC = w.RTPCodecParameters{
		RTPCodecCapability: w.RTPCodecCapability{
			MimeType:     w.MimeTypeOpus,
			ClockRate:    48000,
			Channels:     2,
			SDPFmtpLine:  "",
			RTCPFeedback: nil,
		},
		PayloadType: 111, // this is dynamic; anything between [96-127] is fine
	}
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
	// PREPARE WEBRTC API
	se := w.SettingEngine{}

	// use ice-lite since i don't need NAT traversal
	se.SetLite(true)
	se.SetNetworkTypes([]w.NetworkType{w.NetworkTypeTCP4, w.NetworkTypeUDP4})

	udpMuxCache := map[int]*ice.MultiUDPMuxDefault{}
	tcpMuxCache := map[string]ice.TCPMux{}

	// setup udp mux port
	udpMux, ok := udpMuxCache[UDPPORT]
	if !ok {
		var err error
		udpMux, err = ice.NewMultiUDPMuxFromPort(UDPPORT)
		if err != nil {
			slog.Error("Config error", "err", err)
			os.Exit(1)
		}

		udpMuxCache[UDPPORT] = udpMux
	}

	se.SetICEUDPMux(udpMux)

	// setup tcp mux port
	tcpMux, ok := tcpMuxCache[TCP_MUX_ADDR]
	if !ok {
		tcpAddr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:%d", TCP_MUX_ADDR, TCPPORT))
		if err != nil {
			slog.Error("TCP Listen error", "err", err)
			os.Exit(1)
		}

		tcpListener, err := net.ListenTCP("tcp", tcpAddr)
		if err != nil {
			slog.Error("TCP Listen error", "err", err)
			os.Exit(1)
		}

		tcpMux = w.NewICETCPMux(nil, tcpListener, 8)
		tcpMuxCache[TCP_MUX_ADDR] = tcpMux
	}

	se.SetICETCPMux(tcpMux)

	// set up mediaEngine to take in opus only
	me := &w.MediaEngine{}
	if err := me.RegisterCodec(CODEC, w.RTPCodecTypeAudio); err != nil {
		panic(err) // explode
	}

	// set up interceptors
	ir := &interceptor.Registry{}
	if err := w.RegisterDefaultInterceptors(me, ir); err != nil {
		panic(err) // explode
	}

	API = w.NewAPI(
		w.WithMediaEngine(me),
		w.WithInterceptorRegistry(ir),
		w.WithSettingEngine(se),
	)

	// create the peerConnection
	peerConnection, err := API.NewPeerConnection(w.Configuration{})
	if err != nil {
		panic(err)
	}

	defer func() {
		if closeErr := peerConnection.Close(); closeErr != nil {
			fmt.Printf("cannot close peerConnection: %v\n", closeErr)
		}
	}()

	// set up to store sessions
	s.SessionsManager.Setup()

	// set up the http server
	serverMux := http.NewServeMux()

	// frontend
	ass := http.FileServer(http.FS(assets))
	serverMux.Handle("/", ass)

	// whip for ingest
	serverMux.HandleFunc("/whip", corsHandler(whipHandler))

	// whep for playback
	serverMux.HandleFunc("/whep", corsHandler(whepHandler))

	// start http server
	server := &http.Server{
		Handler: serverMux,
		Addr:    HTTP_ADDRESS,
	}
	log.Fatal(server.ListenAndServe())
}

func checkReqAuth(res http.ResponseWriter, req *http.Request) (tok string, ok bool) {
	tok = req.Header.Get("Authorization")
	if !strings.HasPrefix(tok, "Bearer ") {
		res.WriteHeader(http.StatusUnauthorized)
		res.Write([]byte("Unauthorized"))
		return tok, false
	}

	return tok, true
}
