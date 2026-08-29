package access

import (
	"crypto/subtle"
	"errors"
	"os"
	"time"
)

type Role string

const (
	AdminRole         Role = "ADMIN"
	PermittedUserRole Role = "PERMITTED_USER"
	RecruiterRole     Role = "RECRUITER"
)

var (
	ErrInvalidRegistrationCode  = errors.New("invalid registration code")
	ErrInvalidCodeConfiguration = errors.New("registration codes must be distinct four-digit values")
)

type RegistrationCodes struct {
	PermittedUser string
	Recruiter     string
}

type AudioPolicy struct {
	MaxDuration       time.Duration
	MaxMessagesPerDay int
	MaxMessagesTotal  int
}

func RegistrationCodesFromEnvironment() RegistrationCodes {
	return RegistrationCodes{
		PermittedUser: os.Getenv("PERMITTED_USER_REGISTRATION_CODE"),
		Recruiter:     os.Getenv("RECRUITER_REGISTRATION_CODE"),
	}
}

func (c RegistrationCodes) RoleFor(code string) (Role, error) {
	if !isFourDigit(c.PermittedUser) || !isFourDigit(c.Recruiter) || c.PermittedUser == c.Recruiter {
		return "", ErrInvalidCodeConfiguration
	}
	if isFourDigit(code) && subtle.ConstantTimeCompare([]byte(code), []byte(c.PermittedUser)) == 1 {
		return PermittedUserRole, nil
	}
	if isFourDigit(code) && subtle.ConstantTimeCompare([]byte(code), []byte(c.Recruiter)) == 1 {
		return RecruiterRole, nil
	}
	return "", ErrInvalidRegistrationCode
}

func PolicyFor(role Role) (AudioPolicy, bool) {
	switch role {
	case PermittedUserRole:
		return AudioPolicy{MaxDuration: 30 * time.Second, MaxMessagesPerDay: 10}, true
	case RecruiterRole:
		return AudioPolicy{MaxDuration: 10 * time.Second, MaxMessagesTotal: 3}, true
	default:
		return AudioPolicy{}, false
	}
}

func WelcomeMessage(role Role) (string, bool) {
	switch role {
	case PermittedUserRole:
		return "Welcome to Hadal. You have full access. You can send up to 10 voice notes per day, each up to 30 seconds long, for transcription.", true
	case RecruiterRole:
		return "Welcome to Hadal. You have restricted access. You can send up to three voice notes, each up to 10 seconds long, for transcription. After your third voice note, your access will be permanently disabled.", true
	default:
		return "", false
	}
}

// RegistrationTransition prevents a permitted account from being downgraded.
func RegistrationTransition(current, requested Role) (Role, bool) {
	if current == AdminRole {
		return current, false
	}
	if current == PermittedUserRole && requested == RecruiterRole {
		return current, false
	}
	if current == RecruiterRole && requested == PermittedUserRole {
		return PermittedUserRole, true
	}
	if current == "" {
		return requested, true
	}
	return current, true
}

func isFourDigit(value string) bool {
	if len(value) != 4 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
