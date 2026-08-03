package domain

import "time"

// UserRole defines the authorization level assigned to a user.
type UserRole string

const (
	RoleMember     UserRole = "member"
	RoleAdmin      UserRole = "admin"
	RoleSuperAdmin UserRole = "super_admin"
)

// Permission uses the resource:action convention shared by all inbound adapters.
type Permission string

const (
	PermissionExpensesRead   Permission = "expenses:read"
	PermissionExpensesWrite  Permission = "expenses:write"
	PermissionExpensesDelete Permission = "expenses:delete"
	PermissionBudgetsRead    Permission = "budgets:read"
	PermissionBudgetsWrite   Permission = "budgets:write"
	PermissionAnalyticsRead  Permission = "analytics:read"
	PermissionUsersInvite    Permission = "users:invite"
	PermissionUsersManage    Permission = "users:manage"
	PermissionGroupsManage   Permission = "groups:manage"
	PermissionAuditRead      Permission = "audit:read"
)

// Allows reports whether the role owns a permission.
func (r UserRole) Allows(permission Permission) bool {
	if r == RoleSuperAdmin {
		return true
	}

	switch r {
	case RoleAdmin:
		return permission != PermissionAuditRead
	case RoleMember:
		switch permission {
		case PermissionExpensesRead, PermissionExpensesWrite, PermissionExpensesDelete,
			PermissionBudgetsRead, PermissionBudgetsWrite, PermissionAnalyticsRead:
			return true
		}
	}

	return false
}

// AppUser is the identity projection used by the application layer.
type AppUser struct {
	ID            string
	Email         string
	PasswordHash  string
	DisplayName   string
	Role          UserRole
	AccessBlocked bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Invitation permits a specific email address to create an account.
type Invitation struct {
	ID         string
	Email      string
	Role       UserRole
	TokenHash  string
	Status     InvitationStatus
	InvitedBy  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	AcceptedBy string
	AcceptedAt *time.Time
}

// InvitationStatus describes the invitation lifecycle.
type InvitationStatus string

const (
	InvitationPending  InvitationStatus = "pending"
	InvitationAccepted InvitationStatus = "accepted"
	InvitationRevoked  InvitationStatus = "revoked"
	InvitationExpired  InvitationStatus = "expired"
)

// RefreshToken stores only a hash of a browser refresh token.
type RefreshToken struct {
	ID         string
	UserID     string
	TokenHash  string
	FamilyID   string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	RevokedAt  *time.Time
	ReplacedBy string
}
