package domain

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// checkEnum asserts Valid() for every defined value and for values that are
// not defined, including a defined value in the wrong case.
func checkEnum[T interface {
	~string
	Valid() bool
}](t *testing.T, valid, invalid []T) {
	t.Helper()
	for _, v := range valid {
		if !v.Valid() {
			t.Errorf("%T(%q).Valid() = false, want true", v, string(v))
		}
	}
	for _, v := range invalid {
		if v.Valid() {
			t.Errorf("%T(%q).Valid() = true, want false", v, string(v))
		}
	}
}

func TestUserStatusValid(t *testing.T) {
	checkEnum(t,
		[]UserStatus{UserStatusPendingEmail, UserStatusPendingApproval, UserStatusActive, UserStatusDisabled},
		[]UserStatus{"", "banned", "Active", " active", "pending"})
}

func TestCollectiveKindValid(t *testing.T) {
	checkEnum(t,
		[]CollectiveKind{CollectiveKindColetivo, CollectiveKindProdutora},
		[]CollectiveKind{"", "banda", "Coletivo", "coletivo "})
}

func TestCollectiveStatusValid(t *testing.T) {
	checkEnum(t,
		[]CollectiveStatus{CollectiveStatusPending, CollectiveStatusApproved, CollectiveStatusRejected},
		[]CollectiveStatus{"", "open", "Approved", "active"})
}

func TestMembershipStatusValid(t *testing.T) {
	checkEnum(t,
		[]MembershipStatus{MembershipStatusPending, MembershipStatusApproved, MembershipStatusRejected},
		[]MembershipStatus{"", "open", "Pending", "disabled"})
}

func TestRoleValid(t *testing.T) {
	checkEnum(t,
		[]Role{RoleOwner, RoleMember},
		[]Role{"", "admin", "Owner", "owner "})
}

func TestTokenPurposeValid(t *testing.T) {
	checkEnum(t,
		[]TokenPurpose{TokenPurposeVerifyEmail, TokenPurposeResetPassword, TokenPurposeChangeEmail},
		[]TokenPurpose{"", "login", "Verify_Email", "verify-email"})
}

// allAuditActions lists every defined action, in declaration order.
var allAuditActions = []AuditAction{
	AuditAccountApproved, AuditAccountRejected, AuditAccountBlocked, AuditAccountUnblocked,
	AuditCollectiveRequested, AuditCollectiveApproved, AuditCollectiveRejected,
	AuditCollectiveRenamed, AuditCollectiveBlocks,
	AuditJoinRequested, AuditJoinAdminApproved, AuditJoinOwnerApproved, AuditJoinRejected,
	AuditMemberPromoted, AuditMemberDemoted,
}

func TestAuditActionValid(t *testing.T) {
	checkEnum(t, allAuditActions,
		[]AuditAction{"", "login", "Account_Approved", "account_approved ", "collective_blocks", "join_approved"})
}

// The stored values are part of every backend's data format; changing one
// would orphan existing rows and documents.
func TestEnumValuesAreStable(t *testing.T) {
	for _, tc := range []struct{ want, got string }{
		{"pending_email", string(UserStatusPendingEmail)},
		{"pending_approval", string(UserStatusPendingApproval)},
		{"active", string(UserStatusActive)},
		{"disabled", string(UserStatusDisabled)},
		{"coletivo", string(CollectiveKindColetivo)},
		{"produtora", string(CollectiveKindProdutora)},
		{"pending", string(CollectiveStatusPending)},
		{"approved", string(CollectiveStatusApproved)},
		{"rejected", string(CollectiveStatusRejected)},
		{"pending", string(MembershipStatusPending)},
		{"approved", string(MembershipStatusApproved)},
		{"rejected", string(MembershipStatusRejected)},
		{"owner", string(RoleOwner)},
		{"member", string(RoleMember)},
		{"verify_email", string(TokenPurposeVerifyEmail)},
		{"reset_password", string(TokenPurposeResetPassword)},
		{"change_email", string(TokenPurposeChangeEmail)},
		{"account_approved", string(AuditAccountApproved)},
		{"account_rejected", string(AuditAccountRejected)},
		{"account_blocked", string(AuditAccountBlocked)},
		{"account_unblocked", string(AuditAccountUnblocked)},
		{"collective_requested", string(AuditCollectiveRequested)},
		{"collective_approved", string(AuditCollectiveApproved)},
		{"collective_rejected", string(AuditCollectiveRejected)},
		{"collective_renamed", string(AuditCollectiveRenamed)},
		{"collective_blocks_changed", string(AuditCollectiveBlocks)},
		{"join_requested", string(AuditJoinRequested)},
		{"join_admin_approved", string(AuditJoinAdminApproved)},
		{"join_owner_approved", string(AuditJoinOwnerApproved)},
		{"join_rejected", string(AuditJoinRejected)},
		{"member_promoted", string(AuditMemberPromoted)},
		{"member_demoted", string(AuditMemberDemoted)},
	} {
		if tc.got != tc.want {
			t.Errorf("constant value = %q, want %q", tc.got, tc.want)
		}
	}
}

func TestSentinelErrors(t *testing.T) {
	all := []error{ErrNotFound, ErrEmailTaken, ErrNameTaken, ErrAlreadyMember, ErrConflict}
	seen := map[string]bool{}
	for _, err := range all {
		if !strings.HasPrefix(err.Error(), "domain: ") {
			t.Errorf("%q does not start with %q", err, "domain: ")
		}
		if seen[err.Error()] {
			t.Errorf("message %q is used by two errors", err)
		}
		seen[err.Error()] = true
	}
	for i, a := range all {
		wrapped := fmt.Errorf("create user: %w", a)
		for j, b := range all {
			if got := errors.Is(wrapped, b); got != (i == j) {
				t.Errorf("errors.Is(wrapped %q, %q) = %v, want %v", a, b, got, i == j)
			}
		}
	}
}
