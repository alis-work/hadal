package access

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nyaruka/phonenumbers"
)

const (
	Active   = "ACTIVE"
	Disabled = "DISABLED"
)

var (
	ErrSenderNotFound = errors.New("sender is not registered")
	ErrAdmin          = errors.New("administrators cannot register with an access code")
	ErrRoleDowngrade  = errors.New("a full user cannot become a recruiter")
	ErrInvalidPhone   = errors.New("invalid sender phone number")
)

type Sender struct {
	ID     int64
	Phone  string
	Role   Role
	Status string
}

type Repository interface {
	Get(context.Context, string) (Sender, error)
	Register(context.Context, string, string, Role) (Sender, error)
}

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Get(ctx context.Context, phone string) (Sender, error) {
	var sender Sender
	err := r.pool.QueryRow(ctx, `SELECT id, phone_number, role, access_status FROM whatsapp_senders WHERE phone_number = $1`, phone).Scan(&sender.ID, &sender.Phone, &sender.Role, &sender.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Sender{}, ErrSenderNotFound
	}
	return sender, err
}

func CountryCode(phone string) (string, error) {
	parsed, err := phonenumbers.Parse(phone, "")
	if err != nil || !phonenumbers.IsPossibleNumber(parsed) {
		return "", ErrInvalidPhone
	}
	return "+" + strconv.Itoa(int(parsed.GetCountryCode())), nil
}

func (r *PostgresRepository) Register(ctx context.Context, phone, countryCode string, role Role) (Sender, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Sender{}, err
	}
	defer tx.Rollback(ctx)
	var sender Sender
	err = tx.QueryRow(ctx, `SELECT id, phone_number, role, access_status FROM whatsapp_senders WHERE phone_number = $1 FOR UPDATE`, phone).Scan(&sender.ID, &sender.Phone, &sender.Role, &sender.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO whatsapp_senders (phone_number, country_code, daily_request_limit, role, access_status) VALUES ($1, $2, 10, $3, 'ACTIVE') RETURNING id, phone_number, role, access_status`, phone, countryCode, role).Scan(&sender.ID, &sender.Phone, &sender.Role, &sender.Status)
		if err != nil {
			return Sender{}, err
		}
	} else if err != nil {
		return Sender{}, err
	} else if next, allowed := RegistrationTransition(sender.Role, role); !allowed {
		if sender.Role == AdminRole {
			return Sender{}, ErrAdmin
		}
		return Sender{}, ErrRoleDowngrade
	} else if next == PermittedUserRole && sender.Role == RecruiterRole {
		err = tx.QueryRow(ctx, `UPDATE whatsapp_senders SET role = 'PERMITTED_USER', access_status = 'ACTIVE', recruiter_audio_messages_used = 0, disabled_at = NULL, updated_at = NOW() WHERE id = $1 RETURNING id, phone_number, role, access_status`, sender.ID).Scan(&sender.ID, &sender.Phone, &sender.Role, &sender.Status)
		if err != nil {
			return Sender{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Sender{}, fmt.Errorf("commit registration: %w", err)
	}
	return sender, nil
}
