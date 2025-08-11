package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/webitel/webrtc_recorder/gen/api"
	"github.com/webitel/webrtc_recorder/infra/grpc_client"
)

const (
	LicenseCallManager = "CALL_MANAGER"
	LicenseCallCenter  = "CALL_CENTER"
	LicenseChat        = "CHAT"
	LicenseEmail       = "EMAIL"
	LicenseWFM         = "WFM"
)

func (am *authManager) ProductLimit(ctx context.Context, token, productName string) (int, error) {
	outCtx := grpc_client.WithToken(ctx, token)

	tenant, err := am.customer.API.GetCustomer(outCtx, &api.GetCustomerRequest{})
	if err != nil {
		return 0, err
	}

	if tenant.GetCustomer() == nil {
		return 0, errors.New("")
	}

	var limitMax int32

	for _, grant := range tenant.GetCustomer().GetLicense() {
		if grant.GetProduct() != productName {
			continue // Lookup productName only !
		}

		if errs := grant.GetStatus().GetErrors(); len(errs) != 0 {
			// Also, ignore single 'product exhausted' (remain < 1) error
			// as we do not consider product user assignments here ...
			if len(errs) != 1 || errs[0] != "product exhausted" {
				continue // Currently invalid
			}
		}

		if limitMax < grant.GetRemain() {
			limitMax = grant.GetRemain()
		}
	}

	if limitMax == 0 {
		// FIXME: No CHAT product(s) issued !
		return 0, errors.New("")
	}

	return int(limitMax), nil
}

func (am *authManager) GetSession(c context.Context, token string) (*Session, error) {
	ctx := grpc_client.WithToken(c, token)

	resp, err := am.auth.API.UserInfo(ctx, &api.UserinfoRequest{})
	if err != nil {
		if status.Code(err) == codes.Unauthenticated {
			return nil, ErrStatusUnauthenticated
		}

		return nil, ErrInternal
	}

	if resp == nil {
		return nil, ErrStatusUnauthenticated
	}

	session := &Session{
		ID:         token,
		UserID:     resp.GetUserId(),
		DomainID:   resp.GetDc(),
		DomainName: resp.GetDomain(),
		Expire:     resp.GetExpiresAt(),
		Token:      token,
		RoleIDs:    transformRoles(resp.GetUserId(), resp.GetRoles()), // /FIXME
		Scopes:     transformScopes(resp.GetScope()),
		actions:    make([]string, 0, 1),
		Name:       resp.GetName(),
	}

	session.validLicense, session.active = licenseActiveScope(resp)

	if len(resp.GetPermissions()) > 0 {
		session.adminPermissions = make([]PermissionAccess, len(resp.GetPermissions()), len(resp.GetPermissions()))
		for _, v := range resp.GetPermissions() {
			switch v.GetId() {
			case "add":
				session.adminPermissions = append(session.adminPermissions, PERMISSION_ACCESS_CREATE)
			case "read":
				session.adminPermissions = append(session.adminPermissions, PERMISSION_ACCESS_READ)
			case "write":
				session.adminPermissions = append(session.adminPermissions, PERMISSION_ACCESS_UPDATE)
			case "delete":
				session.adminPermissions = append(session.adminPermissions, PERMISSION_ACCESS_DELETE)
			case "view_cdr_phone_numbers":
				session.actions = append(session.actions, PermissionViewNumbers)
			case "playback_record_file":
				session.actions = append(session.actions, PermissionRecordFile)
			case "time_limited_record_file":
				session.actions = append(session.actions, PermissionTimeLimitedRecordFile)
			case "system_setting":
				session.actions = append(session.actions, PermissionSystemSetting)
			case "scheme_variables":
				session.actions = append(session.actions, PermissionSchemeVariables)
			case "reset_active_attempts":
				session.actions = append(session.actions, PermissionResetActiveAttempts)
			}
		}
	}

	return session, nil
}

// returns the provided original scope
// from all license products assigned to user
//
// NOTE: include <readonly> access
//
//	{ obac:true, access:"r" }
func transformScopes(src []*api.Objclass) []SessionPermission {
	dst := make([]SessionPermission, 0, len(src))

	var access int
	for _, v := range src {
		access, _ = parseAccess(v.GetAccess()) //
		dst = append(dst, SessionPermission{
			ID:   int(v.GetId()),
			Name: v.GetClass(),
			// Abac:   v.Abac,
			Obac:   v.GetObac(),
			rbac:   v.GetRbac(),
			Access: uint32(access),
		})
	}

	return dst
}

// returns the scope from all license products
// active now within their validity boundaries
func licenseActiveScope(src *api.Userinfo) ([]string, []string) {
	var (
		l           = len(src.GetLicense())
		validLicene = make([]string, 0, l)
		now         = time.Now().UnixMilli()
		scope       = make([]string, 0, len(src.GetScope()))
		// canonical name transformations
		objClass = func(name string) string {
			name = strings.TrimSpace(name)
			name = strings.ToLower(name)

			return name
		}
		// indicates whether such `name` exists in scope
		hasScope = func(name string) bool {
			if len(scope) == 0 {
				return name == ""
			}
			// name = objClass(name) // CaseIgnoreMatch(!)
			if len(name) == 0 {
				return true // len(scope) != 0
			}

			e, n := 0, len(scope)
			for ; e < n && scope[e] != name; e++ {
				// break; match found !
			}

			return e < n
		}
		// add unique `setof` to the scope
		addScope = func(setof []string) {
			var name string
			for _, class := range setof {
				name = objClass(class) // CaseIgnoreMatch(!)
				if len(name) == 0 {
					continue
				}

				if !hasScope(name) {
					scope = append(scope, name)
				}
			}
		}
	)
	// gather active only products scopes
	for _, prod := range src.GetLicense() {
		if len(prod.GetScope()) == 0 {
			continue // forceless
		}

		if 0 < prod.GetExpiresAt() && prod.GetExpiresAt() <= now {
			// Expired ! Grant READONLY access
		} else if 0 < prod.GetIssuedAt() && now < prod.GetIssuedAt() {
			// Inactive ! No access grant yet !
		} else {
			// Active ! +OK
			addScope(prod.GetScope())
			validLicene = append(validLicene, prod.GetProd())
		}
	}

	if len(scope) == 0 {
		// ALL License Product(s) are inactive !
		return nil, nil
	}

	var (
		objclass        string
		e, n            = 0, len(src.GetScope())
		caseIgnoreMatch = strings.EqualFold
	)
	for i := 0; i < len(scope); i++ {
		objclass = scope[i]
		for e = 0; e < n && !caseIgnoreMatch(src.GetScope()[e].GetClass(), objclass); e++ {
			// Lookup for caseIgnoreMatch(!) with userinfo.Scope OBAC grants
		}

		if e == n {
			// NOT FOUND ?! OBAC Policy: Access Denied ?!
			scope = append(scope[0:i], scope[i+1:]...)
			i--

			continue
		}
	}

	return validLicene, scope
}

func transformRoles(userID int64, src []*api.ObjectId) []int {
	dst := make([]int, 0, len(src)+1)

	dst = append(dst, int(userID))
	for _, v := range src {
		dst = append(dst, int(v.GetId()))
	}

	return dst
}

func parseAccess(s string) (grants int, err error) {
	// grants = 0 // NoAccess
	var grant int

	for _, c := range s {
		switch c {
		case 'x':
			grant = 8 // XMode
		case 'r':
			grant = 4 // ReadMode
		case 'w':
			grant = 2 // WriteMode
		case 'd':
			grant = 1 // DeleteMode
		default:
			return 0, ErrValidScope
		}

		if (grants & grant) == grant { // grants.HasMode(grant)
			grants |= (grant << 4) // grants.GrantMode(grant)

			continue
		}

		grants |= grant // grants.SetMode(grant)
	}

	return grants, nil
}
