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

func (self *Session) UseRBAC(acc PermissionAccess, perm SessionPermission) bool {
	if !perm.rbac {
		return false
	}

	for _, v := range self.adminPermissions {
		if v == acc {
			return false
		}
	}

	return perm.rbac
}

func (self *Session) GetAclRoles() []int {
	return self.RoleIDs
}

func (self *Session) HasLicense(name string) bool {
	for _, v := range self.validLicense {
		if v == name {
			return true
		}
	}

	return false
}

func (self *Session) GetUserID() int64 {
	return self.UserID
}

func (self *Session) GetDomainID() int64 {
	return self.DomainID
}

func (self *Session) SetIP(ip string) {
	self.userIP.Store(ip)
}

func (self *Session) GetUserIP() string {
	return self.userIP.Load()
}

func (self *Session) HasCallCenterLicense() bool {
	return self.HasLicense(LicenseCallCenter)
}

func (self *Session) HasChatLicense() bool {
	return self.HasLicense(LicenseChat)
}

func (self *Session) CountLicenses() int {
	return len(self.active)
}

func (self *Session) GetPermission(name string) SessionPermission {
	for _, v := range self.Scopes {
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

func (self *Session) IsExpired() bool {
	return self.Expire*1000 < GetMillis()
}

func (self *Session) Trace() map[string]any {
	return map[string]any{"id": self.ID, "domain_id": self.DomainID}
}

func (self *Session) IsValid() error {
	if len(self.ID) < 1 {
		return ErrValidID
	}

	if self.UserID < 1 {
		return ErrValidUserID
	}

	if len(self.Token) < 1 {
		return ErrValidToken
	}

	// if self.DomainId < 1 {
	//	return model.NewBadRequestError("model.session.is_valid.domain_id.app_error", "").SetTranslationParams(self.Trace())
	//}

	if len(self.RoleIDs) < 1 {
		return ErrValidRoleIDs
	}

	return nil
}

func (self *Session) HasAction(name string) bool {
	for _, v := range self.actions {
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
