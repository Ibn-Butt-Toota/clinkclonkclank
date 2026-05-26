package session

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/FARTFARTFARTFARTFARTFARTFARTFARTFARTFRT/clinkclonkclank/track"
	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"
)

var (
	SessionsManager = &SessionManager{}

	APIWHIP *webrtc.API
	APIWHEP *webrtc.API
)

type (
	SessionManager struct {
		SessionCount atomic.Int64
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
func (m *SessionManager) addSession() (s *Session, err error) {
	streamID := uuid.New().String()

	s = &Session{
		streamID:    streamID,
		StreamStart: time.Now(),

		WHEPSessions: map[string]*WHEPSession{},
	}

	s.SetOnClose(func() {
		slog.Info("SessionManager.Session.Done")
		m.sessionsLock.Lock()
		delete(m.sessions, streamID)
		m.sessionsLock.Unlock()
	})

	m.sessionsLock.Lock()
	m.sessions[streamID] = s
	m.sessionsLock.Unlock()

	m.SessionCount.Add(1)
	slog.Info("SessionManager.addSession: Adding", "uid", streamID)

	return s, nil
}

// Get the stream requested, or create it, and add it to the sessions context
func (m *SessionManager) GetOrAddSession(streamID string) (session *Session, err error) {
	session, foundSession := m.GetSessionByID(streamID)

	if !foundSession {
		session, err = m.addSession()
	}

	return session, err
}

// Get Session by id
func (m *SessionManager) GetSessionByID(streamID string) (session *Session, foundSession bool) {
	if streamID == "" {
		return session, foundSession
	}

	m.sessionsLock.RLock()
	defer m.sessionsLock.RUnlock()

	session, foundSession = m.sessions[streamID]
	return session, foundSession
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
