package session

import (
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/FARTFARTFARTFARTFARTFARTFARTFARTFARTFRT/clinkclonkclank/profile"
	"github.com/FARTFARTFARTFARTFARTFARTFARTFARTFARTFRT/clinkclonkclank/track"
	"github.com/pion/webrtc/v4"
)

var (
	SessionsManager *SessionManager

	APIWHIP *webrtc.API
	APIWHEP *webrtc.API
)

type (
	SessionManager struct {
		sessionsLock sync.RWMutex
		sessions     map[string]*Session
	}

	SessionState struct {
		ID string `json:"id"`

		AudioLayerCurrent   string `json:"audioLayerCurrent"`
		AudioTimestamp      uint32 `json:"audioTimestamp"`
		AudioPacketsWritten uint64 `json:"audioPacketsWritten"`
		AudioSequenceNumber uint64 `json:"audioSequenceNumber"`

		VideoLayerCurrent   string `json:"videoLayerCurrent"`
		VideoTimestamp      uint32 `json:"videoTimestamp"`
		VideoBitrate        uint64 `json:"videoBitrate"`
		VideoPacketsDropped uint64 `json:"videoPacketsDropped"`
		VideoPacketsWritten uint64 `json:"videoPacketsWritten"`
		VideoSequenceNumber uint64 `json:"videoSequenceNumber"`
	}

	// Information for a whip session
	StreamSessionState struct {
		StreamKey   string    `json:"streamKey"`
		StreamStart time.Time `json:"streamStart"`

		AudioTracks []track.AudioTrackState `json:"audioTracks"`

		Sessions []SessionState `json:"sessions"`
	}
)

// Prepare the WHIP Session Manager
func (m *SessionManager) Setup() {
	m.sessions = make(map[string]*Session)
}

// Add new session
func (m *SessionManager) addSession(profile profile.PublicProfile) (s *Session, err error) {
	s = &Session{

		StreamKey:   profile.StreamKey,
		StreamStart: time.Now(),

		WHEPSessions: map[string]*WHEPSession{},
	}

	s.SetOnClose(func() {
		slog.Info("SessionManager.Session.Done")
		m.sessionsLock.Lock()
		delete(m.sessions, profile.StreamKey)
		m.sessionsLock.Unlock()
	})

	m.sessionsLock.Lock()
	m.sessions[profile.StreamKey] = s
	m.sessionsLock.Unlock()

	return s, nil
}

// Get the stream requested, or create it, and add it to the sessions context
func (m *SessionManager) GetOrAddSession(profile profile.PublicProfile, isWHIP bool) (session *Session, err error) {
	session, ok := m.GetSessionByID(profile.StreamKey)

	if !ok {
		slog.Info("SessionManager.GetOrAddStream: Adding", "streamKey", profile.StreamKey)
		session, err = m.addSession(profile)
	}

	return session, err
}

// Get Session by id
func (m *SessionManager) GetSessionByID(streamKey string) (session *Session, foundSession bool) {
	slog.Info("SessionManager.GetSessionByID", "streamKey", streamKey)

	m.sessionsLock.RLock()
	defer m.sessionsLock.RUnlock()

	session, foundSession = m.sessions[streamKey]
	return session, foundSession
}

// Gets the current state of all sessions
func (m *SessionManager) GetSessionStates(includePrivateStreams bool) (result []StreamSessionState) {
	slog.Info("SessionManager.GetSessionStates", "isAdmin", includePrivateStreams)
	m.sessionsLock.RLock()
	copiedSessions := make(map[string]*Session)
	maps.Copy(copiedSessions, m.sessions)
	m.sessionsLock.RUnlock()

	for _, s := range copiedSessions {
		s.StatusLock.RLock()

		if !includePrivateStreams && !s.IsPublic {
			s.StatusLock.RUnlock()
			continue
		}

		streamSession := StreamSessionState{
			StreamKey:   s.StreamKey,
			StreamStart: s.StreamStart,
			Sessions:    []SessionState{},
			AudioTracks: []track.AudioTrackState{},
		}

		s.StatusLock.RUnlock()

		host := s.Host.Load()
		if host != nil {
			host.TracksLock.RLock()

			for _, audioTrack := range host.AudioTracks {
				streamSession.AudioTracks = append(
					streamSession.AudioTracks,
					track.AudioTrackState{
						Rid:             audioTrack.Rid,
						PacketsReceived: audioTrack.PacketsReceived.Load(),
						PacketsDropped:  audioTrack.PacketsDropped.Load(),
					})
			}

			host.TracksLock.RUnlock()
		}

		s.WHEPSessionsLock.RLock()
		for _, whep := range s.WHEPSessions {
			if !whep.IsSessionClosed.Load() {
				streamSession.Sessions = append(streamSession.Sessions, whep.GetWHEPSessionStatus())
			}
		}
		s.WHEPSessionsLock.RUnlock()

		result = append(result, streamSession)
	}

	return
}

// Get Session by id
func (m *SessionManager) GetWHEPSessionByID(sessionID string) (whep *WHEPSession, foundSession bool) {
	_, whepSession, foundSession := m.GetSessionAndWHEPByID(sessionID)
	return whepSession, foundSession
}

func (m *SessionManager) GetSessionAndWHEPByID(sessionID string) (streamSession *Session, whepSession *WHEPSession, foundSession bool) {
	m.sessionsLock.RLock()
	defer m.sessionsLock.RUnlock()

	for _, session := range m.sessions {
		session.WHEPSessionsLock.RLock()
		whepSession, ok := session.WHEPSessions[sessionID]
		session.WHEPSessionsLock.RUnlock()
		if ok {
			return session, whepSession, true
		}
	}

	return nil, nil, false
}

func (m *SessionManager) GetSessionByHostSessionID(sessionID string) (session *Session, foundSession bool) {
	m.sessionsLock.RLock()
	defer m.sessionsLock.RUnlock()

	for _, session := range m.sessions {
		host := session.Host.Load()
		if host == nil {
			continue
		}

		if sessionID == host.ID {
			return session, true
		}
	}

	return nil, false
}
