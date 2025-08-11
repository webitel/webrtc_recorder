package auth

import "errors"

var (
	ErrStatusForbidden       = errors.New("forbidden")
	ErrStatusUnauthenticated = errors.New("unauthenticated")
	ErrInternal              = errors.New("internal")
	ErrValidScope            = errors.New("model.session.is_valid.scope.app_error")
	ErrValidID               = errors.New("model.session.is_valid.id.app_error")
	ErrValidUserID           = errors.New("model.session.is_valid.user_id.app_error")
	ErrValidToken            = errors.New("model.session.is_valid.token.app_error")
	ErrValidRoleIDs          = errors.New("model.session.is_valid.role_ids.app_error")
)
