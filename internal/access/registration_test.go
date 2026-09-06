package access

import (
	"errors"
	"testing"
	"time"
)

func TestRoleFor(t *testing.T) {
	codes := RegistrationCodes{PermittedUser: "1234", Recruiter: "5678"}
	role, err := codes.RoleFor("1234")
	if err != nil || role != PermittedUserRole {
		t.Fatalf("permitted code: role=%q err=%v", role, err)
	}
	role, err = codes.RoleFor("5678")
	if err != nil || role != RecruiterRole {
		t.Fatalf("recruiter code: role=%q err=%v", role, err)
	}
}

func TestRoleForRejectsInvalidCodeOrConfiguration(t *testing.T) {
	codes := RegistrationCodes{PermittedUser: "1234", Recruiter: "5678"}
	if _, err := codes.RoleFor("1111"); !errors.Is(err, ErrInvalidRegistrationCode) {
		t.Fatalf("unexpected invalid-code error: %v", err)
	}
	if _, err := (RegistrationCodes{PermittedUser: "1234", Recruiter: "1234"}).RoleFor("1234"); !errors.Is(err, ErrInvalidCodeConfiguration) {
		t.Fatalf("unexpected invalid-configuration error: %v", err)
	}
}

func TestWelcomeMessage(t *testing.T) {
	message, ok := WelcomeMessage(PermittedUserRole)
	if !ok || message != "Welcome to Hadal. You have 10 translation requests per day across text and voice. Your limit resets at midnight UTC. Voice notes can be up to 30 seconds." {
		t.Fatalf("unexpected permitted welcome message: %q", message)
	}
	message, ok = WelcomeMessage(RecruiterRole)
	if !ok || message != "Welcome to Hadal. You have 3 translation requests total across text and voice. This limit does not reset. Voice notes can be up to 10 seconds." {
		t.Fatalf("unexpected recruiter welcome message: %q", message)
	}
}

func TestRegistrationTransition(t *testing.T) {
	if _, allowed := RegistrationTransition(PermittedUserRole, RecruiterRole); allowed {
		t.Fatal("a full user must not be downgraded to recruiter")
	}
	if role, allowed := RegistrationTransition(RecruiterRole, PermittedUserRole); !allowed || role != PermittedUserRole {
		t.Fatalf("a recruiter must be able to upgrade: role=%q allowed=%t", role, allowed)
	}
}

func TestCountryCode(t *testing.T) {
	countryCode, err := CountryCode("+447700900111")
	if err != nil || countryCode != "+44" {
		t.Fatalf("unexpected country code: %q err=%v", countryCode, err)
	}
}

func TestPolicyFor(t *testing.T) {
	permitted, ok := PolicyFor(PermittedUserRole)
	if !ok || permitted.MaxDuration != 30*time.Second || permitted.MaxMessagesPerDay != 10 || permitted.MaxMessagesTotal != 0 {
		t.Fatalf("unexpected permitted policy: %+v", permitted)
	}
	recruiter, ok := PolicyFor(RecruiterRole)
	if !ok || recruiter.MaxDuration != 10*time.Second || recruiter.MaxMessagesPerDay != 0 || recruiter.MaxMessagesTotal != 3 {
		t.Fatalf("unexpected recruiter policy: %+v", recruiter)
	}
}

func TestRegistrationTransitionProtectsPermittedUsersAndAllowsRecruiterUpgrade(t *testing.T) {
	if role, allowed := RegistrationTransition(PermittedUserRole, RecruiterRole); role != PermittedUserRole || allowed {
		t.Fatalf("permitted user downgrade: role=%q allowed=%v", role, allowed)
	}
	if role, allowed := RegistrationTransition(RecruiterRole, PermittedUserRole); role != PermittedUserRole || !allowed {
		t.Fatalf("recruiter upgrade: role=%q allowed=%v", role, allowed)
	}
	if _, allowed := RegistrationTransition(AdminRole, PermittedUserRole); allowed {
		t.Fatal("admin registration must be rejected")
	}
}

func TestTesterCanSwitchRoles(t *testing.T) {
	const tester = "+447700900111"
	if !testerCanSwitch(tester, tester) {
		t.Fatal("configured tester should be able to switch roles")
	}
	if testerCanSwitch(tester, "") || testerCanSwitch("+447700900112", tester) {
		t.Fatal("tester exception must not apply to an empty or different number")
	}
}
