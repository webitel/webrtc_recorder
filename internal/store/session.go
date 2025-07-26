package store

import (
	"github.com/hashicorp/golang-lru/v2/simplelru"
	"github.com/pkg/errors"
	"github.com/webitel/webrtc_recorder/internal/model"
	"github.com/webitel/wlog"
)

const (
	cacheClientsSize = 2000
)

var ErrSessionNotFound = errors.New("session not found in cache")

type SessionStore struct {
	log  *wlog.Logger
	sess *simplelru.LRU[string, model.RtcUploadVideoSession]
}

func NewSessionStore(log *wlog.Logger) *SessionStore {
	sess, err := simplelru.NewLRU[string, model.RtcUploadVideoSession](cacheClientsSize, nil)
	if err != nil { // size < 0
		panic(err.Error())
	}

	return &SessionStore{
		log:  log,
		sess: sess,
	}
}

func (s *SessionStore) Get(id string) (model.RtcUploadVideoSession, error) {
	sess, ok := s.sess.Get(id)
	if ok {
		s.log.Debug("session cache hit", wlog.String("session_id", id))
		return sess, nil
	}
	s.log.Debug("session cache miss", wlog.String("session_id", id))
	return nil, ErrSessionNotFound
}

func (s *SessionStore) Remove(id string) bool {
	ok := s.sess.Remove(id)
	if !ok {
		s.log.Debug("session cache miss", wlog.String("session_id", id))
	}
	return ok
}

func (s *SessionStore) Add(id string, sess model.RtcUploadVideoSession) error {
	s.log.Debug("adding new session to cache", wlog.String("session_id", id))
	s.sess.Add(id, sess)
	return nil
}
