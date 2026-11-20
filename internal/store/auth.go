package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	applicationOwnerRoleID = "00000000-0000-4000-8000-000000000101"
	platformAdminRoleID    = "00000000-0000-4000-8000-000000000001"
)

type User struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
}

type Session struct {
	User      User
	ExpiresAt time.Time
}

func (store *Store) UsersExist(ctx context.Context) (bool, error) {
	var exists bool
	if err := store.Pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM users)").Scan(&exists); err != nil {
		return false, fmt.Errorf("check users: %w", err)
	}
	return exists, nil
}

func (store *Store) BootstrapAdmin(ctx context.Context, email, displayName, password string) (bool, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return false, err
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || len(displayName) > 120 {
		return false, errors.New("bootstrap display name must contain 1 to 120 characters")
	}
	if len(password) < 12 || len(password) > 72 {
		return false, errors.New("bootstrap password must contain 12 to 72 bytes")
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return false, fmt.Errorf("hash bootstrap password: %w", err)
	}
	userID, err := randomUUID()
	if err != nil {
		return false, err
	}
	applicationID, err := randomUUID()
	if err != nil {
		return false, err
	}

	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, fmt.Errorf("begin bootstrap: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck

	if _, err := transaction.Exec(ctx, "SELECT pg_advisory_xact_lock(742198532)"); err != nil {
		return false, fmt.Errorf("lock bootstrap: %w", err)
	}
	var usersExist bool
	if err := transaction.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM users)").Scan(&usersExist); err != nil {
		return false, fmt.Errorf("check bootstrap state: %w", err)
	}
	if usersExist {
		return false, nil
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash)
		VALUES ($1, $2, $3, $4)`, userID, email, displayName, string(passwordHash)); err != nil {
		return false, fmt.Errorf("create bootstrap user: %w", err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO user_platform_roles (user_id, role_id) VALUES ($1, $2)`, userID, platformAdminRoleID); err != nil {
		return false, fmt.Errorf("assign platform administrator: %w", err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO applications (id, name, slug, created_by) VALUES ($1, 'Default', 'default', $2)`, applicationID, userID); err != nil {
		return false, fmt.Errorf("create default application: %w", err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO application_memberships (application_id, user_id, role_id)
		VALUES ($1, $2, $3)`, applicationID, userID, applicationOwnerRoleID); err != nil {
		return false, fmt.Errorf("assign default application owner: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit bootstrap: %w", err)
	}
	return true, nil
}

func (store *Store) Login(ctx context.Context, email, password string, lifetime time.Duration) (Session, string, string, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return Session{}, "", "", ErrInvalidCredentials
	}
	var session Session
	var passwordHash string
	var disabledAt *time.Time
	err = store.Pool.QueryRow(ctx, `
		SELECT id, email, display_name, password_hash, disabled_at
		FROM users
		WHERE email = $1`, email).Scan(
		&session.User.ID,
		&session.User.Email,
		&session.User.DisplayName,
		&passwordHash,
		&disabledAt,
	)
	if errors.Is(err, pgx.ErrNoRows) || disabledAt != nil {
		return Session{}, "", "", ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, "", "", fmt.Errorf("find login user: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
		return Session{}, "", "", ErrInvalidCredentials
	}

	sessionToken, err := randomToken()
	if err != nil {
		return Session{}, "", "", err
	}
	csrfToken, err := randomToken()
	if err != nil {
		return Session{}, "", "", err
	}
	session.ExpiresAt = time.Now().UTC().Add(lifetime)
	sessionHash := sha256.Sum256([]byte(sessionToken))
	csrfHash := sha256.Sum256([]byte(csrfToken))
	if _, err := store.Pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, csrf_token_hash, expires_at)
		VALUES ($1, $2, $3, $4)`, sessionHash[:], session.User.ID, csrfHash[:], session.ExpiresAt); err != nil {
		return Session{}, "", "", fmt.Errorf("create session: %w", err)
	}
	return session, sessionToken, csrfToken, nil
}

func (store *Store) Authenticate(ctx context.Context, sessionToken string) (Session, []byte, error) {
	if sessionToken == "" {
		return Session{}, nil, ErrInvalidCredentials
	}
	tokenHash := sha256.Sum256([]byte(sessionToken))
	var session Session
	var csrfHash []byte
	err := store.Pool.QueryRow(ctx, `
		SELECT users.id, users.email, users.display_name, sessions.expires_at, sessions.csrf_token_hash
		FROM sessions
		JOIN users ON users.id = sessions.user_id
		WHERE sessions.token_hash = $1
		  AND sessions.expires_at > now()
		  AND users.disabled_at IS NULL`, tokenHash[:]).Scan(
		&session.User.ID,
		&session.User.Email,
		&session.User.DisplayName,
		&session.ExpiresAt,
		&csrfHash,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, nil, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, nil, fmt.Errorf("authenticate session: %w", err)
	}
	return session, csrfHash, nil
}

func (store *Store) Logout(ctx context.Context, sessionToken string) error {
	tokenHash := sha256.Sum256([]byte(sessionToken))
	if _, err := store.Pool.Exec(ctx, "DELETE FROM sessions WHERE token_hash = $1", tokenHash[:]); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func VerifyCSRF(provided string, expectedHash []byte) bool {
	providedHash := sha256.Sum256([]byte(provided))
	return len(expectedHash) == len(providedHash) && subtle.ConstantTimeCompare(providedHash[:], expectedHash) == 1
}

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value {
		return "", errors.New("invalid email address")
	}
	return value, nil
}

func randomToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
