package session

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/FARTFARTFARTFARTFARTFARTFARTFARTFARTFRT/clinkclonkclank/track"
	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"
)

type (
	Session struct {
		// Protects streamID, MOTD, HasHost, IsPublic
		StatusLock sync.RWMutex
		streamID   string

		HasHost     atomic.Bool
		StreamStart time.Time

		Host atomic.Pointer[WHIPSession]

		closeOnce sync.Once
		onClose   func()

		// TODO: if you have a token + auth service, add the token to the session so you can auth when getting an existing session.
		// myTok tokType

		// Protects WHEPSessions
		WHEPSessionsLock sync.RWMutex
		WHEPSessions     map[string]*WHEPSession
	}
)

func (session *Session) SetOnClose(onClose func()) {
	session.onClose = onClose
}

// Add WHEP viewer session
func (s *Session) AddWHEP(whepSessionID string, peerConnection *webrtc.PeerConnection, audioTrack *track.AudioTrack, RTCPSender *webrtc.RTPSender) (err error) {
	slog.Info("WHIPSessionManager.WHIPSession.AddWHEPSession")

	whepSession := CreateNewWHEPSession(
		whepSessionID,
		s.streamID,
		audioTrack,
		peerConnection,
	)

	whepSession.SetOnClose(s.handleWHEPClose)

	s.WHEPSessionsLock.Lock()
	s.WHEPSessions[whepSessionID] = whepSession
	s.WHEPSessionsLock.Unlock()
	s.updateHostWHEPSessionsSnapshot()

	return nil
}

// Add host
func (s *Session) AddHost(peerConnection *webrtc.PeerConnection) (err error) {
	slog.Info("Session.AddHost")

	for {
		host := s.Host.Load()
		if host == nil {
			break
		}

		if host.PeerConnection.ConnectionState() != webrtc.PeerConnectionStateClosed {
			return fmt.Errorf("session already has a host")
		}

		if s.Host.CompareAndSwap(host, nil) {
			break
		}
	}

	host := &WHIPSession{
		ID:          uuid.New().String(),
		AudioTracks: make(map[string]*track.AudioTrack),
	}
	host.SetOnClosed(s.handleHostClosed)

	host.AddPeerConnection(peerConnection, host.ID)
	if !s.Host.CompareAndSwap(nil, host) {
		host.RemovePeerConnection()
		host.RemoveTracks()
		return fmt.Errorf("session already has a host")
	}
	s.resetWHEPSessionsForNewHost()
	host.WHEPSessionsSnapshot.Store(make(map[string]*WHEPSession))
	s.updateHostWHEPSessionsSnapshot()
	s.HasHost.Store(true)

	return nil
}

func (s *Session) RemoveHost() {
	host := s.Host.Swap(nil)
	if host == nil {
		slog.Info("Session.RemoveHost", "streamID", s.streamID, "msg", "No host to remove")
		return
	}

	slog.Info("Session.RemoveHost", "streamKey", s.streamID)
	s.HasHost.Store(false)

	host.WHEPSessionsSnapshot.Store(make(map[string]*WHEPSession))
	host.RemovePeerConnection()
	host.RemoveTracks()
}

func (s *Session) handleWHEPClose(whepSessionID string) {
	slog.Info("Session.HandleWHEPClose", "streamID", s.streamID, "whepSessionID", whepSessionID)

	s.WHEPSessionsLock.Lock()
	_, ok := s.WHEPSessions[whepSessionID]
	if ok {
		delete(s.WHEPSessions, whepSessionID)
	}
	s.WHEPSessionsLock.Unlock()

	if !ok {
		return
	}

	s.updateHostWHEPSessionsSnapshot()

	if s.isEmpty() {
		s.close()
	}
}

func (s *Session) handleHostClosed() {
	s.RemoveHost()

	if s.isEmpty() {
		s.close()
	}
}

// Remove all Hosts and clients before closing down session
func (s *Session) close() {
	s.closeOnce.Do(func() {
		s.WHEPSessionsLock.Lock()
		whepSessions := make([]*WHEPSession, 0, len(s.WHEPSessions))
		for _, whepSession := range s.WHEPSessions {
			whepSessions = append(whepSessions, whepSession)
		}
		s.WHEPSessions = make(map[string]*WHEPSession)
		s.WHEPSessionsLock.Unlock()

		for _, whepSession := range whepSessions {
			whepSession.Close()
		}
		s.updateHostWHEPSessionsSnapshot()

		s.RemoveHost()

		if s.onClose != nil {
			s.onClose()
		}
	})
}

// Returns true is no WHIP tracks are present, and no WHEP sessions are waiting for incoming streams
func (s *Session) isEmpty() bool {
	if s.hasWHEPSessions() {
		slog.Info("Session.IsEmpty.HasWHEPSessions (false)", "streamID", s.streamID)
		return false
	}

	if s.isStreaming() {
		slog.Info("Session.IsEmpty.IsActive (false)", "streamID", s.streamID)
		return false
	}

	slog.Info("Session.IsEmpty (true)", "streamID", s.streamID)
	return true
}

// Returns true if any tracks are available for the session
func (s *Session) isStreaming() bool {

	host := s.Host.Load()
	if host == nil {
		return false
	}

	host.TracksLock.RLock()

	if len(host.AudioTracks) != 0 {
		slog.Info("Session.IsActive.AudioTracks", "count", len(host.AudioTracks))
		host.TracksLock.RUnlock()
		return true
	}

	host.TracksLock.RUnlock()
	return false
}

func (s *Session) hasWHEPSessions() bool {
	s.WHEPSessionsLock.RLock()
	slog.Info("Session.HasWHEPSessions", "count", len(s.WHEPSessions))

	if len(s.WHEPSessions) == 0 {
		s.WHEPSessionsLock.RUnlock()
		return false
	}

	s.WHEPSessionsLock.RUnlock()
	return true
}

func (s *Session) updateHostWHEPSessionsSnapshot() {
	host := s.Host.Load()
	if host == nil {
		return
	}

	s.WHEPSessionsLock.RLock()
	snapshot := make(map[string]*WHEPSession, len(s.WHEPSessions))
	for _, whepSession := range s.WHEPSessions {
		if !whepSession.IsSessionClosed.Load() {
			snapshot[whepSession.SessionID] = whepSession
		}
	}
	s.WHEPSessionsLock.RUnlock()

	host.WHEPSessionsSnapshot.Store(snapshot)
}

func (s *Session) resetWHEPSessionsForNewHost() {
	s.WHEPSessionsLock.RLock()
	for _, whepSession := range s.WHEPSessions {
		if whepSession == nil {
			continue
		}

		whepSession.ResetForNewPublisher()
	}
	s.WHEPSessionsLock.RUnlock()
}
