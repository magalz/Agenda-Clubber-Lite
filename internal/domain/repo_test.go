package domain

import "testing"

func TestMembershipViewPromotesMembershipFields(t *testing.T) {
	v := MembershipView{
		Membership:       Membership{UserID: "u1", CollectiveID: "c1", Role: RoleOwner, Status: MembershipStatusApproved},
		CollectiveName:   "Coletivo Ação",
		CollectiveKind:   CollectiveKindColetivo,
		CollectiveStatus: CollectiveStatusApproved,
	}
	if v.UserID != "u1" || v.CollectiveID != "c1" || v.Role != RoleOwner || v.Status != MembershipStatusApproved {
		t.Errorf("promoted fields = %+v", v.Membership)
	}
	if err := v.Validate(); err != nil {
		t.Errorf("Validate through the embedded Membership: %v", err)
	}
}
