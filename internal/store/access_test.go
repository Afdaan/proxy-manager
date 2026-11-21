package store

import "testing"

func TestValidateManagedUser(t *testing.T) {
	email, displayName, memberships, err := validateManagedUser(
		" ADMIN@Example.com ",
		"  Gateway Admin  ",
		[]MembershipInput{{ApplicationID: "app-1", RoleID: "role-1"}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if email != "admin@example.com" || displayName != "Gateway Admin" || len(memberships) != 1 {
		t.Fatalf("unexpected normalized user: %q %q %#v", email, displayName, memberships)
	}
}

func TestValidateManagedUserRejectsDuplicateApplicationMembership(t *testing.T) {
	_, _, _, err := validateManagedUser(
		"admin@example.com",
		"Admin",
		[]MembershipInput{
			{ApplicationID: "app-1", RoleID: "role-1"},
			{ApplicationID: "app-1", RoleID: "role-2"},
		},
	)
	if err == nil {
		t.Fatal("expected duplicate application membership to fail")
	}
}

func TestValidatePassword(t *testing.T) {
	if err := validatePassword("twelve-chars"); err != nil {
		t.Fatalf("expected valid password: %v", err)
	}
	if err := validatePassword("short"); err == nil {
		t.Fatal("expected short password to fail")
	}
}
