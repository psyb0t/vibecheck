package postgresql

import "errors"

var (
	ErrHostnameRequired = errors.New("hostname is required")
	ErrPortRequired     = errors.New("port is required")
	ErrUsernameRequired = errors.New("username is required")
	ErrPasswordRequired = errors.New("password is required")
	ErrDatabaseRequired = errors.New("database is required")
)
