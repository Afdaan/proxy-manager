package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

const accessManagementLockID = 742198534

type ApplicationRole struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

type UserMembership struct {
	ApplicationID   string `json:"applicationId"`
	ApplicationName string `json:"applicationName"`
	RoleID          string `json:"roleId"`
	RoleName        string `json:"roleName"`
}

type ManagedUser struct {
	ID            string           `json:"id"`
	Email         string           `json:"email"`
	DisplayName   string           `json:"displayName"`
	Enabled       bool             `json:"enabled"`
	PlatformAdmin bool             `json:"platformAdmin"`
	Memberships   []UserMembership `json:"memberships"`
	Version       int64            `json:"version"`
	CreatedAt     time.Time        `json:"createdAt"`
	LastLoginAt   *time.Time       `json:"lastLoginAt,omitempty"`
}

type AccessAuditLog struct {
	ID           int64           `json:"id"`
	ActorEmail   string          `json:"actorEmail"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resourceType"`
	ResourceID   string          `json:"resourceId,omitempty"`
	SourceIP     string          `json:"sourceIp,omitempty"`
	Details      json.RawMessage `json:"details"`
	CreatedAt    time.Time       `json:"createdAt"`
}

type AccessOverview struct {
	Applications []Application     `json:"applications"`
	Roles        []ApplicationRole `json:"roles"`
	Users        []ManagedUser     `json:"users"`
	AuditLogs    []AccessAuditLog  `json:"auditLogs"`
}

type MembershipInput struct {
	ApplicationID string `json:"applicationId"`
	RoleID        string `json:"roleId"`
}

type CreateManagedUserParams struct {
	Email         string
	DisplayName   string
	Password      string
	PlatformAdmin bool
	Memberships   []MembershipInput
	RequestID     string
	SourceIP      string
}

type UpdateManagedUserParams struct {
	ID            string
	Email         string
	DisplayName   string
	Enabled       bool
	PlatformAdmin bool
	Memberships   []MembershipInput
	Version       int64
	RequestID     string
	SourceIP      string
}

func (store *Store) AccessOverview(ctx context.Context, actorUserID string) (AccessOverview, error) {
	allowed, err := isPlatformAdministrator(ctx, store.Pool, actorUserID)
	if err != nil {
		return AccessOverview{}, err
	}
	if !allowed {
		return AccessOverview{}, ErrForbidden
	}

	applications, err := store.listAllApplications(ctx)
	if err != nil {
		return AccessOverview{}, err
	}
	roles, err := store.listApplicationRoles(ctx)
	if err != nil {
		return AccessOverview{}, err
	}
	users, err := store.listManagedUsers(ctx)
	if err != nil {
		return AccessOverview{}, err
	}
	auditLogs, err := store.listAccessAuditLogs(ctx, 50)
	if err != nil {
		return AccessOverview{}, err
	}
	return AccessOverview{
		Applications: applications,
		Roles:        roles,
		Users:        users,
		AuditLogs:    auditLogs,
	}, nil
}

func (store *Store) CreateManagedUser(ctx context.Context, actorUserID string, params CreateManagedUserParams) (ManagedUser, error) {
	email, displayName, memberships, err := validateManagedUser(params.Email, params.DisplayName, params.Memberships)
	if err != nil {
		return ManagedUser{}, err
	}
	if !params.PlatformAdmin && len(memberships) == 0 {
		return ManagedUser{}, errors.New("an enabled user requires platform or application access")
	}
	if err := validatePassword(params.Password); err != nil {
		return ManagedUser{}, err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(params.Password), 12)
	if err != nil {
		return ManagedUser{}, fmt.Errorf("hash password: %w", err)
	}
	userID, err := randomUUID()
	if err != nil {
		return ManagedUser{}, err
	}

	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ManagedUser{}, fmt.Errorf("begin user create: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck
	if err := lockAccessManagement(ctx, transaction); err != nil {
		return ManagedUser{}, err
	}
	allowed, err := isPlatformAdministrator(ctx, transaction, actorUserID)
	if err != nil {
		return ManagedUser{}, err
	}
	if !allowed {
		return ManagedUser{}, ErrForbidden
	}

	if _, err := transaction.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash)
		VALUES ($1, $2, $3, $4)`, userID, email, displayName, string(passwordHash)); err != nil {
		if isUniqueViolation(err) {
			return ManagedUser{}, ErrConflict
		}
		return ManagedUser{}, fmt.Errorf("create user: %w", err)
	}
	if params.PlatformAdmin {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO user_platform_roles (user_id, role_id) VALUES ($1, $2)`, userID, platformAdminRoleID); err != nil {
			return ManagedUser{}, fmt.Errorf("assign platform administrator: %w", err)
		}
	}
	if err := replaceMemberships(ctx, transaction, userID, memberships); err != nil {
		return ManagedUser{}, err
	}
	if err := insertAuditLog(ctx, transaction, actorUserID, "user.create", "user", userID, params.RequestID, params.SourceIP, map[string]any{
		"email":         email,
		"platformAdmin": params.PlatformAdmin,
		"memberships":   len(memberships),
	}); err != nil {
		return ManagedUser{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return ManagedUser{}, fmt.Errorf("commit user create: %w", err)
	}
	return store.managedUserByID(ctx, userID)
}

func (store *Store) UpdateManagedUser(ctx context.Context, actorUserID string, params UpdateManagedUserParams) (ManagedUser, error) {
	email, displayName, memberships, err := validateManagedUser(params.Email, params.DisplayName, params.Memberships)
	if err != nil {
		return ManagedUser{}, err
	}
	if params.Enabled && !params.PlatformAdmin && len(memberships) == 0 {
		return ManagedUser{}, errors.New("an enabled user requires platform or application access")
	}
	if params.Version < 1 {
		return ManagedUser{}, errors.New("version must be at least 1")
	}

	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ManagedUser{}, fmt.Errorf("begin user update: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck
	if err := lockAccessManagement(ctx, transaction); err != nil {
		return ManagedUser{}, err
	}
	allowed, err := isPlatformAdministrator(ctx, transaction, actorUserID)
	if err != nil {
		return ManagedUser{}, err
	}
	if !allowed {
		return ManagedUser{}, ErrForbidden
	}

	var wasEnabled bool
	var wasPlatformAdmin bool
	err = transaction.QueryRow(ctx, `
		SELECT disabled_at IS NULL,
		       EXISTS (
		           SELECT 1 FROM user_platform_roles
		           WHERE user_id = users.id AND role_id = $2
		       )
		FROM users
		WHERE id = $1
		FOR UPDATE`, params.ID, platformAdminRoleID).Scan(&wasEnabled, &wasPlatformAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedUser{}, ErrNotFound
	}
	if err != nil {
		return ManagedUser{}, fmt.Errorf("lock managed user: %w", err)
	}
	if actorUserID == params.ID && (!params.Enabled || !params.PlatformAdmin) {
		return ManagedUser{}, ErrSelfAccessChange
	}
	if wasEnabled && wasPlatformAdmin && (!params.Enabled || !params.PlatformAdmin) {
		remaining, err := countOtherEnabledAdministrators(ctx, transaction, params.ID)
		if err != nil {
			return ManagedUser{}, err
		}
		if remaining == 0 {
			return ManagedUser{}, ErrLastAdministrator
		}
	}

	command, err := transaction.Exec(ctx, `
		UPDATE users
		SET email = $2,
		    display_name = $3,
		    disabled_at = CASE WHEN $4 THEN NULL ELSE COALESCE(disabled_at, now()) END,
		    version = version + 1,
		    updated_at = now()
		WHERE id = $1 AND version = $5`, params.ID, email, displayName, params.Enabled, params.Version)
	if err != nil {
		if isUniqueViolation(err) {
			return ManagedUser{}, ErrConflict
		}
		return ManagedUser{}, fmt.Errorf("update managed user: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ManagedUser{}, ErrStaleVersion
	}

	if params.PlatformAdmin {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO user_platform_roles (user_id, role_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, params.ID, platformAdminRoleID); err != nil {
			return ManagedUser{}, fmt.Errorf("assign platform administrator: %w", err)
		}
	} else if _, err := transaction.Exec(ctx, `
		DELETE FROM user_platform_roles WHERE user_id = $1 AND role_id = $2`, params.ID, platformAdminRoleID); err != nil {
		return ManagedUser{}, fmt.Errorf("remove platform administrator: %w", err)
	}
	if err := replaceMemberships(ctx, transaction, params.ID, memberships); err != nil {
		return ManagedUser{}, err
	}
	if !params.Enabled {
		if _, err := transaction.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1", params.ID); err != nil {
			return ManagedUser{}, fmt.Errorf("revoke disabled user sessions: %w", err)
		}
	}
	if err := insertAuditLog(ctx, transaction, actorUserID, "user.update", "user", params.ID, params.RequestID, params.SourceIP, map[string]any{
		"email":         email,
		"enabled":       params.Enabled,
		"platformAdmin": params.PlatformAdmin,
		"memberships":   len(memberships),
	}); err != nil {
		return ManagedUser{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return ManagedUser{}, fmt.Errorf("commit user update: %w", err)
	}
	return store.managedUserByID(ctx, params.ID)
}

func (store *Store) ResetManagedUserPassword(ctx context.Context, actorUserID, targetUserID, password, requestID, sourceIP string) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin password reset: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck
	if err := lockAccessManagement(ctx, transaction); err != nil {
		return err
	}
	allowed, err := isPlatformAdministrator(ctx, transaction, actorUserID)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	command, err := transaction.Exec(ctx, `
		UPDATE users
		SET password_hash = $2,
		    password_changed_at = now(),
		    version = version + 1,
		    updated_at = now()
		WHERE id = $1`, targetUserID, string(passwordHash))
	if err != nil {
		return fmt.Errorf("reset user password: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := transaction.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1", targetUserID); err != nil {
		return fmt.Errorf("revoke password reset sessions: %w", err)
	}
	if err := insertAuditLog(ctx, transaction, actorUserID, "user.password_reset", "user", targetUserID, requestID, sourceIP, map[string]any{
		"sessionsRevoked": true,
	}); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit password reset: %w", err)
	}
	return nil
}

func (store *Store) listAllApplications(ctx context.Context) ([]Application, error) {
	rows, err := store.Pool.Query(ctx, `
		SELECT id, name, slug, true
		FROM applications
		ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list access applications: %w", err)
	}
	defer rows.Close()
	applications := make([]Application, 0)
	for rows.Next() {
		var application Application
		if err := rows.Scan(&application.ID, &application.Name, &application.Slug, &application.CanWrite); err != nil {
			return nil, fmt.Errorf("scan access application: %w", err)
		}
		applications = append(applications, application)
	}
	return applications, rows.Err()
}

func (store *Store) listApplicationRoles(ctx context.Context) ([]ApplicationRole, error) {
	rows, err := store.Pool.Query(ctx, `
		SELECT roles.id,
		       roles.name,
		       COALESCE(jsonb_agg(role_permissions.permission_name ORDER BY role_permissions.permission_name), '[]'::jsonb)
		FROM roles
		LEFT JOIN role_permissions ON role_permissions.role_id = roles.id
		WHERE roles.scope = 'application'
		GROUP BY roles.id
		ORDER BY roles.name`)
	if err != nil {
		return nil, fmt.Errorf("list application roles: %w", err)
	}
	defer rows.Close()
	roles := make([]ApplicationRole, 0)
	for rows.Next() {
		var role ApplicationRole
		var permissionsJSON []byte
		if err := rows.Scan(&role.ID, &role.Name, &permissionsJSON); err != nil {
			return nil, fmt.Errorf("scan application role: %w", err)
		}
		if err := json.Unmarshal(permissionsJSON, &role.Permissions); err != nil {
			return nil, fmt.Errorf("decode application role permissions: %w", err)
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func (store *Store) listManagedUsers(ctx context.Context) ([]ManagedUser, error) {
	rows, err := store.Pool.Query(ctx, managedUsersQuery+" ORDER BY users.created_at, users.email")
	if err != nil {
		return nil, fmt.Errorf("list managed users: %w", err)
	}
	defer rows.Close()
	users := make([]ManagedUser, 0)
	for rows.Next() {
		user, err := scanManagedUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (store *Store) managedUserByID(ctx context.Context, userID string) (ManagedUser, error) {
	user, err := scanManagedUser(store.Pool.QueryRow(ctx, managedUsersQuery+" HAVING users.id = $1", userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedUser{}, ErrNotFound
	}
	return user, err
}

const managedUsersQuery = `
	SELECT users.id,
	       users.email,
	       users.display_name,
	       users.disabled_at IS NULL,
	       EXISTS (
	           SELECT 1 FROM user_platform_roles
	           WHERE user_id = users.id AND role_id = '00000000-0000-4000-8000-000000000001'
	       ),
	       COALESCE(
	           jsonb_agg(
	               jsonb_build_object(
	                   'applicationId', memberships.application_id,
	                   'applicationName', applications.name,
	                   'roleId', roles.id,
	                   'roleName', roles.name
	               ) ORDER BY applications.name
	           ) FILTER (WHERE memberships.application_id IS NOT NULL),
	           '[]'::jsonb
	       ),
	       users.version,
	       users.created_at,
	       users.last_login_at
	FROM users
	LEFT JOIN application_memberships memberships ON memberships.user_id = users.id
	LEFT JOIN applications ON applications.id = memberships.application_id
	LEFT JOIN roles ON roles.id = memberships.role_id
	GROUP BY users.id`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanManagedUser(row rowScanner) (ManagedUser, error) {
	var user ManagedUser
	var membershipsJSON []byte
	if err := row.Scan(
		&user.ID,
		&user.Email,
		&user.DisplayName,
		&user.Enabled,
		&user.PlatformAdmin,
		&membershipsJSON,
		&user.Version,
		&user.CreatedAt,
		&user.LastLoginAt,
	); err != nil {
		return ManagedUser{}, err
	}
	if err := json.Unmarshal(membershipsJSON, &user.Memberships); err != nil {
		return ManagedUser{}, fmt.Errorf("decode user memberships: %w", err)
	}
	return user, nil
}

func (store *Store) listAccessAuditLogs(ctx context.Context, limit int) ([]AccessAuditLog, error) {
	rows, err := store.Pool.Query(ctx, `
		SELECT audit_logs.id,
		       COALESCE(users.email, 'system'),
		       audit_logs.action,
		       audit_logs.resource_type,
		       COALESCE(audit_logs.resource_id, ''),
		       COALESCE(audit_logs.source_ip::text, ''),
		       audit_logs.details,
		       audit_logs.created_at
		FROM audit_logs
		LEFT JOIN users ON users.id = audit_logs.actor_user_id
		ORDER BY audit_logs.created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list access audit logs: %w", err)
	}
	defer rows.Close()
	logs := make([]AccessAuditLog, 0)
	for rows.Next() {
		var log AccessAuditLog
		if err := rows.Scan(&log.ID, &log.ActorEmail, &log.Action, &log.ResourceType, &log.ResourceID, &log.SourceIP, &log.Details, &log.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan access audit log: %w", err)
		}
		logs = append(logs, log)
	}
	return logs, rows.Err()
}

func validateManagedUser(emailValue, displayNameValue string, memberships []MembershipInput) (string, string, []MembershipInput, error) {
	email, err := normalizeEmail(emailValue)
	if err != nil {
		return "", "", nil, err
	}
	displayName := strings.TrimSpace(displayNameValue)
	if displayName == "" || len(displayName) > 120 {
		return "", "", nil, errors.New("displayName must contain 1 to 120 characters")
	}
	normalized := make([]MembershipInput, 0, len(memberships))
	seenApplications := make(map[string]struct{}, len(memberships))
	for _, membership := range memberships {
		membership.ApplicationID = strings.TrimSpace(membership.ApplicationID)
		membership.RoleID = strings.TrimSpace(membership.RoleID)
		if membership.ApplicationID == "" || membership.RoleID == "" {
			return "", "", nil, errors.New("membership applicationId and roleId are required")
		}
		if _, exists := seenApplications[membership.ApplicationID]; exists {
			return "", "", nil, errors.New("only one role can be assigned per application")
		}
		seenApplications[membership.ApplicationID] = struct{}{}
		normalized = append(normalized, membership)
	}
	return email, displayName, normalized, nil
}

func validatePassword(password string) error {
	if len(password) < 12 || len(password) > 72 {
		return errors.New("password must contain 12 to 72 bytes")
	}
	return nil
}

func replaceMemberships(ctx context.Context, transaction pgx.Tx, userID string, memberships []MembershipInput) error {
	if _, err := transaction.Exec(ctx, "DELETE FROM application_memberships WHERE user_id = $1", userID); err != nil {
		return fmt.Errorf("clear application memberships: %w", err)
	}
	for _, membership := range memberships {
		command, err := transaction.Exec(ctx, `
			INSERT INTO application_memberships (application_id, user_id, role_id)
			SELECT applications.id, $2, roles.id
			FROM applications
			JOIN roles ON roles.id = $3 AND roles.scope = 'application'
			WHERE applications.id = $1`, membership.ApplicationID, userID, membership.RoleID)
		if err != nil {
			return fmt.Errorf("assign application membership: %w", err)
		}
		if command.RowsAffected() == 0 {
			return errors.New("membership application or role does not exist")
		}
	}
	return nil
}

type permissionQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func isPlatformAdministrator(ctx context.Context, querier permissionQuerier, userID string) (bool, error) {
	var allowed bool
	if err := querier.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1
		    FROM user_platform_roles
		    JOIN role_permissions ON role_permissions.role_id = user_platform_roles.role_id
		    JOIN users ON users.id = user_platform_roles.user_id
		    WHERE user_platform_roles.user_id = $1
		      AND users.disabled_at IS NULL
		      AND role_permissions.permission_name = 'platform.admin'
		)`, userID).Scan(&allowed); err != nil {
		return false, fmt.Errorf("check platform administrator: %w", err)
	}
	return allowed, nil
}

func countOtherEnabledAdministrators(ctx context.Context, transaction pgx.Tx, excludedUserID string) (int, error) {
	var count int
	if err := transaction.QueryRow(ctx, `
		SELECT COUNT(DISTINCT users.id)
		FROM users
		JOIN user_platform_roles ON user_platform_roles.user_id = users.id
		JOIN role_permissions ON role_permissions.role_id = user_platform_roles.role_id
		WHERE users.id <> $1
		  AND users.disabled_at IS NULL
		  AND role_permissions.permission_name = 'platform.admin'`, excludedUserID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count enabled administrators: %w", err)
	}
	return count, nil
}

func lockAccessManagement(ctx context.Context, transaction pgx.Tx) error {
	if _, err := transaction.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", accessManagementLockID); err != nil {
		return fmt.Errorf("lock access management: %w", err)
	}
	return nil
}

func insertAuditLog(ctx context.Context, transaction pgx.Tx, actorUserID, action, resourceType, resourceID, requestID, sourceIP string, details map[string]any) error {
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_logs (
		    actor_user_id, action, resource_type, resource_id, request_id, source_ip, details
		) VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::inet, $7)`,
		actorUserID, action, resourceType, resourceID, requestID, sourceIP, details); err != nil {
		return fmt.Errorf("write access audit log: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && databaseError.Code == "23505"
}
