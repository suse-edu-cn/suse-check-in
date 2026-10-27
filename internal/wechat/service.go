package wechat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"sync"
	"time"

	"suse-checkin/internal/store"
	"suse-checkin/internal/upstream"
)

const (
	StatusWaiting = "waiting"
	StatusScanned = "scanned"
	StatusSuccess = "success"
	StatusExpired = "expired"
	StatusError   = "error"
)

var (
	ErrNotFound = errors.New("扫码登录会话不存在或已过期")
	ErrNotReady = errors.New("扫码尚未完成")
)

type LoginView struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	QRCode    string    `json:"qrCode,omitempty"`
	StudentID string    `json:"studentId,omitempty"`
	Name      string    `json:"name,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type CompleteInput struct {
	AccountID int64
	Name      string
	Campus    string
}

type loginSession struct {
	id          string
	ownerID     uint64
	status      string
	message     string
	login       *upstream.WechatLogin
	qrExpiresAt time.Time
	keepUntil   time.Time
	result      upstream.WechatLoginResult
	cancel      context.CancelFunc
	createdAt   time.Time
}

type Service struct {
	client *upstream.Client
	store  *store.Store
	logger *log.Logger

	mu    sync.Mutex
	items map[string]*loginSession
}

func New(client *upstream.Client, st *store.Store, logger *log.Logger) *Service {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Service{
		client: client,
		store:  st,
		logger: logger,
		items:  make(map[string]*loginSession),
	}
}

func (s *Service) Start(ctx context.Context, ownerID uint64) (LoginView, error) {
	s.cleanup()
	login, err := s.client.StartWechatLogin(ctx)
	if err != nil {
		return LoginView{}, err
	}
	id, err := newID()
	if err != nil {
		return LoginView{}, err
	}
	now := time.Now()
	waitCtx, cancel := context.WithDeadline(context.Background(), login.ExpiresAt())
	sess := &loginSession{
		id:          id,
		ownerID:     ownerID,
		status:      StatusWaiting,
		login:       login,
		qrExpiresAt: login.ExpiresAt(),
		keepUntil:   login.ExpiresAt().Add(5 * time.Minute),
		cancel:      cancel,
		createdAt:   now,
	}
	s.mu.Lock()
	s.items[id] = sess
	view := viewOf(sess)
	s.mu.Unlock()

	go s.wait(sess, waitCtx)
	return view, nil
}

func (s *Service) Status(id string, ownerID uint64) (LoginView, error) {
	s.cleanup()
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.items[id]
	if sess == nil || sess.ownerID != ownerID {
		return LoginView{}, ErrNotFound
	}
	if sess.status == StatusWaiting || sess.status == StatusScanned {
		if !time.Now().Before(sess.qrExpiresAt) {
			sess.status = StatusExpired
			sess.message = upstream.ErrWechatQRExpired.Error()
			sess.cancel()
		}
	}
	return viewOf(sess), nil
}

func (s *Service) Complete(id string, ownerID uint64, in CompleteInput) (store.AccountView, error) {
	s.mu.Lock()
	sess := s.items[id]
	if sess == nil || sess.ownerID != ownerID {
		s.mu.Unlock()
		return store.AccountView{}, ErrNotFound
	}
	if sess.status != StatusSuccess {
		message := sess.message
		s.mu.Unlock()
		if message == "" {
			message = ErrNotReady.Error()
		}
		return store.AccountView{}, errors.New(message)
	}
	result := sess.result
	s.mu.Unlock()

	name := in.Name
	if name == "" {
		name = result.Name
	}
	var (
		view store.AccountView
		err  error
	)
	if in.AccountID > 0 {
		acc, ok := s.store.Account(ownerID, in.AccountID)
		if !ok {
			return store.AccountView{}, store.ErrNotFound
		}
		if acc.LoginType != store.LoginWechat || acc.StudentID != result.StudentID {
			return store.AccountView{}, errors.New("该账号不是对应的扫码登录账号")
		}
		view, err = s.store.UpdateWechatSession(ownerID, acc.ID, name, in.Campus, result.Cookies, result.ExpiresAt)
	} else {
		view, err = s.store.AddWechatAccount(ownerID, store.WechatAccountInput{
			StudentID: result.StudentID,
			Name:      name,
			Campus:    in.Campus,
			Cookies:   result.Cookies,
			ExpiresAt: result.ExpiresAt,
		})
	}
	if err != nil {
		return store.AccountView{}, err
	}
	s.Cancel(id, ownerID)
	return view, nil
}

func (s *Service) Cancel(id string, ownerID uint64) {
	s.mu.Lock()
	sess := s.items[id]
	if sess == nil || sess.ownerID != ownerID {
		s.mu.Unlock()
		return
	}
	delete(s.items, id)
	sess.cancel()
	s.mu.Unlock()
}

func (s *Service) Close() {
	s.mu.Lock()
	items := s.items
	s.items = make(map[string]*loginSession)
	for _, sess := range items {
		sess.cancel()
	}
	s.mu.Unlock()
}

func (s *Service) wait(sess *loginSession, ctx context.Context) {
	result, err := sess.login.Wait(ctx, func(status int) {
		if status == 1 {
			s.mu.Lock()
			if s.items[sess.id] == sess && sess.status == StatusWaiting {
				sess.status = StatusScanned
			}
			s.mu.Unlock()
		}
	})
	if err != nil {
		s.mu.Lock()
		if s.items[sess.id] == sess {
			if errors.Is(err, upstream.ErrWechatQRExpired) {
				sess.status = StatusExpired
			} else {
				sess.status = StatusError
			}
			sess.message = err.Error()
		}
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	if s.items[sess.id] == sess {
		sess.status = StatusSuccess
		sess.message = ""
		sess.result = result
		sess.keepUntil = time.Now().Add(5 * time.Minute)
	}
	s.mu.Unlock()
}

func (s *Service) cleanup() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.items {
		if now.After(sess.keepUntil) {
			sess.cancel()
			delete(s.items, id)
		}
	}
}

func viewOf(sess *loginSession) LoginView {
	view := LoginView{
		ID:        sess.id,
		Status:    sess.status,
		Message:   sess.message,
		ExpiresAt: sess.qrExpiresAt,
	}
	if sess.status == StatusWaiting || sess.status == StatusScanned {
		view.QRCode = sess.login.QRCode()
	}
	if sess.status == StatusSuccess {
		view.StudentID = sess.result.StudentID
		view.Name = sess.result.Name
	}
	return view
}

func newID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
