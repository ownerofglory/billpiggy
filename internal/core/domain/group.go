package domain

import "time"

// UserGroup is a private sharing boundary created by an administrator.
type UserGroup struct {
	// ID uniquely identifies the group.
	ID string
	// Name is the administrator-visible group name.
	Name string
	// CreatedBy identifies the administrator who owns the group.
	CreatedBy string
	// CreatedAt records when the group was created.
	CreatedAt time.Time
	// MemberIDs lists users permitted to view shared items.
	MemberIDs []string
}
