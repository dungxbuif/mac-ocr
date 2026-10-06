package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"macocr/proxy/domain"
	"macocr/proxy/internal/native"
	"macocr/proxy/internal/notifications"
	"time"
)

type WorkerRepository struct {
	pool   *pgxpool.Pool
	cipher *notifications.SecretCipher
}

func NewWorkerRepository(pool *pgxpool.Pool, cipher *notifications.SecretCipher) *WorkerRepository {
	return &WorkerRepository{pool, cipher}
}
func enrollmentHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (r *WorkerRepository) CreateEnrollment(ctx context.Context, token string, expires time.Time) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO native_enrollments(token_hash,expires_at)VALUES($1,$2)`, enrollmentHash(token), expires)
	return err
}
func (r *WorkerRepository) Enroll(ctx context.Context, token, nodeID, name, secret string) error {
	encrypted, err := r.cipher.Encrypt(secret)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE native_enrollments SET consumed_at=now() WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at>now()`, enrollmentHash(token))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrUnauthorized
	}
	_, err = tx.Exec(ctx, `INSERT INTO native_workers(node_id,name,secret)VALUES($1,$2,$3) ON CONFLICT(node_id) DO UPDATE SET name=EXCLUDED.name,secret=EXCLUDED.secret,revoked=false,desired_control='resume'`, nodeID, name, encrypted)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *WorkerRepository) Secret(ctx context.Context, nodeID string) (string, error) {
	var encrypted []byte
	err := r.pool.QueryRow(ctx, `SELECT secret FROM native_workers WHERE node_id=$1 AND NOT revoked`, nodeID).Scan(&encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrUnauthorized
	}
	if err != nil {
		return "", err
	}
	return r.cipher.Decrypt(encrypted)
}
func (r *WorkerRepository) ListWorkers(ctx context.Context) ([]native.WorkerNode, error) {
	rows, err := r.pool.Query(ctx, `SELECT node_id,name,revoked,metadata,desired_control FROM native_workers ORDER BY created_at DESC LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []native.WorkerNode{}
	for rows.Next() {
		var n native.WorkerNode
		n.State = "offline"
		n.Capabilities = []string{}
		var metadata []byte
		if err := rows.Scan(&n.NodeID, &n.Name, &n.Revoked, &metadata, &n.ControlAction); err != nil {
			return nil, err
		}
		n.Metadata = &native.WorkerMetadata{}
		if err := json.Unmarshal(metadata, n.Metadata); err != nil {
			return nil, err
		}
		n.Paused = n.ControlAction != "resume"
		out = append(out, n)
	}
	return out, rows.Err()
}

func (r *WorkerRepository) SetDesiredControl(ctx context.Context, nodeID, action string) error {
	if action != "pause" && action != "resume" && action != "drain" {
		return domain.ErrBadParamInput
	}
	tag, err := r.pool.Exec(ctx, `UPDATE native_workers SET desired_control=$2 WHERE node_id=$1 AND NOT revoked`, nodeID, action)
	if err == nil && tag.RowsAffected() != 1 {
		return domain.ErrNotFound
	}
	return err
}

func (r *WorkerRepository) DesiredControl(ctx context.Context, nodeID string) (string, error) {
	var action string
	err := r.pool.QueryRow(ctx, `SELECT desired_control FROM native_workers WHERE node_id=$1 AND NOT revoked`, nodeID).Scan(&action)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return action, err
}
func (r *WorkerRepository) Revoke(ctx context.Context, nodeID string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE native_workers SET revoked=true WHERE node_id=$1`, nodeID)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}
func (r *WorkerRepository) Assign(ctx context.Context, route native.WorkerRoute) error {
	tag, err := r.pool.Exec(ctx, `INSERT INTO native_worker_routes(attempt_id,node_id,document_id,kind) SELECT $1,$2,$3,$4 FROM documents d,native_workers n WHERE d.id=$3 AND d.attempt_id=$1 AND d.status='processing' AND n.node_id=$2 AND NOT n.revoked`, route.AttemptID, route.NodeID, route.DocumentID, route.Kind)
	if err == nil && tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return err
}
func (r *WorkerRepository) AssignedNode(ctx context.Context, docID, attemptID string) (string, error) {
	var nodeID string
	err := r.pool.QueryRow(ctx, `SELECT node_id FROM native_worker_routes WHERE document_id=$1 AND attempt_id=$2`, docID, attemptID).Scan(&nodeID)
	return nodeID, err
}
func (r *WorkerRepository) Routes(ctx context.Context) ([]native.WorkerRoute, error) {
	rows, err := r.pool.Query(ctx, `SELECT r.node_id,r.document_id,r.attempt_id,r.kind,CASE WHEN d.attempt_id IS DISTINCT FROM r.attempt_id THEN 'expired' ELSE d.status END,r.created_at FROM native_worker_routes r JOIN documents d ON d.id=r.document_id ORDER BY r.created_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []native.WorkerRoute{}
	for rows.Next() {
		var item native.WorkerRoute
		if err := rows.Scan(&item.NodeID, &item.DocumentID, &item.AttemptID, &item.Kind, &item.Status, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Existing revoked identities are never silently reactivated by a shared key.
func (r *WorkerRepository) RegisterShared(ctx context.Context, nodeID, secret string) error {
	encrypted, err := r.cipher.Encrypt(secret)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO native_workers(node_id,name,secret)VALUES($1,$1,$2) ON CONFLICT(node_id)DO NOTHING`, nodeID, encrypted)
	return err
}

// Metadata is reported by an authenticated worker and returned only through
// the session-protected admin roster. Never include it in public responses.
func (r *WorkerRepository) UpdateMetadata(ctx context.Context, nodeID string, metadata native.WorkerMetadata) error {
	if !native.ValidWorkerMetadata(metadata) {
		return domain.ErrBadParamInput
	}
	body, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `UPDATE native_workers SET metadata=$2 WHERE node_id=$1 AND NOT revoked`, nodeID, body)
	if err == nil && tag.RowsAffected() != 1 {
		return domain.ErrUnauthorized
	}
	return err
}
