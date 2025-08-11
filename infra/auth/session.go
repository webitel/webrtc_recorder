package auth

import (
	"context"
	"time"

	"go.uber.org/atomic"
	"golang.org/x/sync/singleflight"

	"github.com/webitel/wlog"
)

var sessionGroupRequest singleflight.Group

const tokenRequestTimeout = time.Second * 15

type Session struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	DomainID   int64         `json:"domain_id"`
	DomainName string        `json:"domain_name"`
	Expire     int64         `json:"expire"`
	UserID     int64         `json:"user_id"`
	userIP     atomic.String `json:"user_ip"`
	RoleIDs    []int         `json:"role_ids"`

	Token            string              `json:"token"`
	Scopes           []SessionPermission `json:"scopes"`
	active           []string            `json:"-"`
	adminPermissions []PermissionAccess
	actions          []string
	validLicense     []string
}

func (s *Session) UseRBAC(acc PermissionAccess, perm SessionPermission) bool {
	if !perm.rbac {
		return false
	}

	for _, v := range s.adminPermissions {
		if v == acc {
			return false
		}
	}

	return perm.rbac
}

func (s *Session) GetAclRoles() []int {
	return s.RoleIDs
}

func (s *Session) HasLicense(name string) bool {
	for _, v := range s.validLicense {
		if v == name {
			return true
		}
	}

	return false
}

func (s *Session) GetUserID() int64 {
	return s.UserID
}

func (s *Session) GetDomainID() int64 {
	return s.DomainID
}

func (s *Session) SetIP(ip string) {
	s.userIP.Store(ip)
}

func (s *Session) GetUserIP() string {
	return s.userIP.Load()
}

func (s *Session) HasCallCenterLicense() bool {
	return s.HasLicense(LicenseCallCenter)
}

func (s *Session) HasChatLicense() bool {
	return s.HasLicense(LicenseChat)
}

func (s *Session) CountLicenses() int {
	return len(s.active)
}

func (s *Session) GetPermission(name string) SessionPermission {
	for _, v := range s.Scopes {
		if v.Name == name {
			return v
		}
	}

	return NotAllowPermission(name)
}

func NotAllowPermission(name string) SessionPermission {
	return SessionPermission{
		ID:     0,
		Name:   name,
		Obac:   true,
		rbac:   true,
		Access: 0,
	}
}

// GetMillis is a convenience method to get milliseconds since epoch.
func GetMillis() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

func (s *Session) IsExpired() bool {
	return s.Expire*1000 < GetMillis()
}

func (s *Session) Trace() map[string]any {
	return map[string]any{"id": s.ID, "domain_id": s.DomainID}
}

func (s *Session) IsValid() error {
	if len(s.ID) < 1 {
		return ErrValidID
	}

	if s.UserID < 1 {
		return ErrValidUserID
	}

	if len(s.Token) < 1 {
		return ErrValidToken
	}

	// if self.DomainId < 1 {
	//	return model.NewBadRequestError("model.session.is_valid.domain_id.app_error", "").SetTranslationParams(self.Trace())
	// }

	if len(s.RoleIDs) < 1 {
		return ErrValidRoleIDs
	}

	return nil
}

func (s *Session) HasAction(name string) bool {
	for _, v := range s.actions {
		if v == name {
			return true
		}
	}

	return false
}

func (am *authManager) getSession(c context.Context, token string) (Session, error) {
	if v, ok := am.session.Get(token); ok {
		return *v, nil
	}

	result, err, shared := sessionGroupRequest.Do(token, func() (any, error) {
		ctx, cancel := context.WithTimeout(c, tokenRequestTimeout)
		defer cancel()

		return am.GetSession(ctx, token)
	})
	if err != nil {
		return Session{}, err
	}

	session := result.(*Session)

	if !shared {
		am.session.Add(token, session)
		am.log.With(wlog.String("user_name", session.Name)).Debug("store")
	}

	return *session, nil
}
