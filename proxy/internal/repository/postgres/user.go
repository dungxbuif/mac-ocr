package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"macocr/proxy/domain"
)

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

func (r *UserRepository) Create(ctx context.Context, u *domain.User) (*domain.User, error) {
	if strings.TrimSpace(u.Email) == "" {
		return nil, domain.ErrBadParamInput
	}
	role := u.Role
	if role == "" {
		role = domain.RoleUser
	}

	var out domain.User
	var roleStr string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO users (email, role, password_hash, disabled)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, email, role, password_hash, disabled, created_at, updated_at`,
		strings.TrimSpace(strings.ToLower(u.Email)), role, u.PasswordHash, u.Disabled,
	).Scan(&out.ID, &out.Email, &roleStr, &out.PasswordHash, &out.Disabled, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return nil, fmt.Errorf("%w: user with email %q already exists", domain.ErrConflict, u.Email)
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}
	out.Role = domain.Role(roleStr)
	return &out, nil
}

func (r *UserRepository) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	var u domain.User
	var roleStr string
	err := r.pool.QueryRow(ctx,
		`SELECT id, email, role, COALESCE(password_hash, ''), disabled, created_at, updated_at
		 FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &roleStr, &u.PasswordHash, &u.Disabled, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select user by id: %w", err)
	}
	u.Role = domain.Role(roleStr)
	return &u, nil
}

func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	var u domain.User
	var roleStr string
	err := r.pool.QueryRow(ctx,
		`SELECT id, email, role, COALESCE(password_hash, ''), disabled, created_at, updated_at
		 FROM users WHERE email = $1`, strings.TrimSpace(strings.ToLower(email)),
	).Scan(&u.ID, &u.Email, &roleStr, &u.PasswordHash, &u.Disabled, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select user by email: %w", err)
	}
	u.Role = domain.Role(roleStr)
	return &u, nil
}

func (r *UserRepository) List(ctx context.Context, limit, offset int) ([]domain.User, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := r.pool.Query(ctx,
		`SELECT id, email, role, COALESCE(password_hash, ''), disabled, created_at, updated_at
		 FROM users ORDER BY id ASC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	users := []domain.User{}
	for rows.Next() {
		var u domain.User
		var roleStr string
		if err := rows.Scan(&u.ID, &u.Email, &roleStr, &u.PasswordHash, &u.Disabled, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		u.Role = domain.Role(roleStr)
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *UserRepository) Update(ctx context.Context, u *domain.User) (*domain.User, error) {
	var out domain.User
	var roleStr string
	err := r.pool.QueryRow(ctx,
		`UPDATE users
		 SET email = $1, role = $2, password_hash = $3, disabled = $4, updated_at = now()
		 WHERE id = $5
		 RETURNING id, email, role, password_hash, disabled, created_at, updated_at`,
		strings.TrimSpace(strings.ToLower(u.Email)), string(u.Role), u.PasswordHash, u.Disabled, u.ID,
	).Scan(&out.ID, &out.Email, &roleStr, &out.PasswordHash, &out.Disabled, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return nil, fmt.Errorf("%w: email %q already in use", domain.ErrConflict, u.Email)
		}
		return nil, fmt.Errorf("update user: %w", err)
	}
	out.Role = domain.Role(roleStr)
	return &out, nil
}
