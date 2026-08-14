package tests

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"macocr/proxy/domain"
)

type mockUserRepoFull struct {
	users map[int64]*domain.User
}

func newMockUserRepo() *mockUserRepoFull {
	return &mockUserRepoFull{users: make(map[int64]*domain.User)}
}
func (m *mockUserRepoFull) Create(ctx context.Context, u *domain.User) (*domain.User, error) {
	m.users[u.ID] = u
	return u, nil
}
func (m *mockUserRepoFull) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return u, nil
}
func (m *mockUserRepoFull) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	for _, u := range m.users {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (m *mockUserRepoFull) List(ctx context.Context, limit, offset int) ([]domain.User, error) {
	return nil, nil
}
func (m *mockUserRepoFull) Update(ctx context.Context, u *domain.User) (*domain.User, error) {
	m.users[u.ID] = u
	return u, nil
}

type mockConfigRepoFull struct {
	configs      map[int64]*domain.AccountConfig
	reservations map[string]domain.UploadReservation
}

func newMockConfigRepo() *mockConfigRepoFull {
	return &mockConfigRepoFull{configs: make(map[int64]*domain.AccountConfig), reservations: make(map[string]domain.UploadReservation)}
}
func (m *mockConfigRepoFull) ReserveUpload(_ context.Context, reservation domain.UploadReservation) error {
	cfg, ok := m.configs[reservation.UserID]
	if !ok {
		return domain.ErrNotFound
	}
	if cfg.StorageQuotaBytes > 0 && cfg.StorageUsedBytes+cfg.StorageReservedBytes+reservation.SizeBytes > cfg.StorageQuotaBytes {
		return domain.ErrStorageQuotaExceeded
	}
	cfg.StorageReservedBytes += reservation.SizeBytes
	m.reservations[reservation.ObjectKey] = reservation
	return nil
}
func (m *mockConfigRepoFull) ReleaseUpload(_ context.Context, userID int64, objectKey string) (bool, error) {
	reservation, ok := m.reservations[objectKey]
	if !ok || reservation.UserID != userID {
		return false, nil
	}
	if cfg := m.configs[userID]; cfg != nil {
		cfg.StorageReservedBytes -= reservation.SizeBytes
	}
	delete(m.reservations, objectKey)
	return true, nil
}
func (m *mockConfigRepoFull) ListExpiredUploads(_ context.Context, before time.Time, limit int) ([]domain.UploadReservation, error) {
	var out []domain.UploadReservation
	for _, item := range m.reservations {
		if !item.ExpiresAt.After(before) && len(out) < limit {
			out = append(out, item)
		}
	}
	return out, nil
}
func (m *mockConfigRepoFull) consumeInputs(userID int64, docs []domain.Document) error {
	cfg := m.configs[userID]
	var bytesToUse, reservedToConsume int64
	for _, doc := range docs {
		bytesToUse += doc.InputSizeBytes
		if reservation, ok := m.reservations[doc.InputKey]; ok {
			if reservation.UserID != userID || reservation.SizeBytes != doc.InputSizeBytes {
				return domain.ErrInvalidURL
			}
			reservedToConsume += reservation.SizeBytes
		} else if strings.HasPrefix(doc.InputKey, fmt.Sprintf("uploads/%d/", userID)) {
			return domain.ErrInvalidURL
		}
	}
	if cfg.StorageQuotaBytes > 0 && cfg.StorageUsedBytes+cfg.StorageReservedBytes-reservedToConsume+bytesToUse > cfg.StorageQuotaBytes {
		return domain.ErrStorageQuotaExceeded
	}
	cfg.StorageUsedBytes += bytesToUse
	cfg.StorageReservedBytes -= reservedToConsume
	for _, doc := range docs {
		delete(m.reservations, doc.InputKey)
	}
	return nil
}
func (m *mockConfigRepoFull) GetByUserID(ctx context.Context, userID int64) (*domain.AccountConfig, error) {
	cfg, ok := m.configs[userID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return cfg, nil
}
func (m *mockConfigRepoFull) Update(ctx context.Context, cfg *domain.AccountConfig) (*domain.AccountConfig, error) {
	m.configs[cfg.UserID] = cfg
	return cfg, nil
}
func (m *mockConfigRepoFull) ReserveDocs(ctx context.Context, userID int64, count int64) error {
	cfg, ok := m.configs[userID]
	if !ok {
		return domain.ErrNotFound
	}
	if cfg.DocQuota > 0 && cfg.DocUsed+count > cfg.DocQuota {
		return domain.ErrQuotaExceeded
	}
	cfg.DocUsed += count
	return nil
}
func (m *mockConfigRepoFull) RefundDocs(ctx context.Context, userID int64, count int64) error {
	cfg, ok := m.configs[userID]
	if !ok {
		return domain.ErrNotFound
	}
	cfg.DocUsed -= count
	if cfg.DocUsed < 0 {
		cfg.DocUsed = 0
	}
	return nil
}
func (m *mockConfigRepoFull) ResetDocUsed(ctx context.Context, userID int64) error {
	if cfg, ok := m.configs[userID]; ok {
		cfg.DocUsed = 0
	}
	return nil
}

type mockKeyRepoFull struct {
	keys   map[int64]*domain.ApiKey
	byHash map[string]*domain.ApiKey
	nextID int64
}

func newMockKeyRepoFull() *mockKeyRepoFull {
	return &mockKeyRepoFull{
		keys:   make(map[int64]*domain.ApiKey),
		byHash: make(map[string]*domain.ApiKey),
		nextID: 1,
	}
}

func (m *mockKeyRepoFull) Create(ctx context.Context, k *domain.ApiKey) (*domain.ApiKey, error) {
	k.ID = m.nextID
	m.nextID++
	k.CreatedAt = time.Now()
	m.keys[k.ID] = k
	m.byHash[k.KeyHash] = k
	return k, nil
}

func (m *mockKeyRepoFull) ListByUser(ctx context.Context, userID int64) ([]domain.ApiKey, error) {
	var res []domain.ApiKey
	for _, k := range m.keys {
		if k.UserID == userID {
			res = append(res, *k)
		}
	}
	return res, nil
}

func (m *mockKeyRepoFull) GetByHash(ctx context.Context, hash string) (*domain.ApiKey, error) {
	k, ok := m.byHash[hash]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return k, nil
}

func (m *mockKeyRepoFull) Revoke(ctx context.Context, id int64) error {
	k, ok := m.keys[id]
	if !ok {
		return domain.ErrNotFound
	}
	now := time.Now()
	k.RevokedAt = &now
	return nil
}

type mockObjectRepoFull struct {
	stored     map[string][]byte
	presignErr error
}

func newMockObjectRepoFull() *mockObjectRepoFull {
	return &mockObjectRepoFull{stored: make(map[string][]byte)}
}

func (m *mockObjectRepoFull) Put(ctx context.Context, key string, body io.Reader, contentType string) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.stored[key] = b
	return nil
}

func (m *mockObjectRepoFull) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	b, ok := m.stored[key]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *mockObjectRepoFull) Exists(ctx context.Context, key string) (bool, error) {
	_, ok := m.stored[key]
	return ok, nil
}

func (m *mockObjectRepoFull) PresignGetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return "http://mock-s3/" + key, nil
}

func (m *mockObjectRepoFull) PresignPutURL(ctx context.Context, key, contentType string, contentLength int64, ttl time.Duration) (string, http.Header, error) {
	if m.presignErr != nil {
		return "", nil, m.presignErr
	}
	return "http://mock-s3/" + key + "?put=1", http.Header{"Content-Type": []string{contentType}}, nil
}

func (m *mockObjectRepoFull) SourceURLForKey(key string) string {
	return "s3://macocr-inputs/" + key
}

func (m *mockObjectRepoFull) KeyFromOwnURL(rawURL string, userID int64) (string, bool, error) {
	prefix := "s3://macocr-inputs/"
	if !strings.HasPrefix(rawURL, prefix) {
		return "", false, nil
	}
	key := strings.TrimPrefix(rawURL, prefix)
	if !strings.HasPrefix(key, fmt.Sprintf("uploads/%d/", userID)) {
		return "", true, domain.ErrNotFound
	}
	return key, true, nil
}

func (m *mockObjectRepoFull) Stat(ctx context.Context, key string) (*domain.ObjectInfo, error) {
	b, ok := m.stored[key]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &domain.ObjectInfo{Key: key, SizeBytes: int64(len(b))}, nil
}

func (m *mockObjectRepoFull) Ping(ctx context.Context) error {
	return nil
}

func (m *mockObjectRepoFull) Delete(_ context.Context, key string) error {
	delete(m.stored, key)
	return nil
}

type mockDocRepoAdapter struct {
	docs    map[string]*domain.Document
	byUser  map[int64][]*domain.Document
	configs *mockConfigRepoFull
}

func newMockDocRepoAdapter() *mockDocRepoAdapter {
	return &mockDocRepoAdapter{
		docs:   make(map[string]*domain.Document),
		byUser: make(map[int64][]*domain.Document),
	}
}

func (m *mockDocRepoAdapter) Create(ctx context.Context, doc *domain.Document) (*domain.Document, error) {
	d := *doc
	d.CreatedAt = time.Now()
	d.UpdatedAt = time.Now()
	m.docs[d.ID] = &d
	m.byUser[d.UserID] = append(m.byUser[d.UserID], &d)
	return &d, nil
}
func (m *mockDocRepoAdapter) CreateWithQuota(ctx context.Context, doc *domain.Document) (*domain.Document, error) {
	if m.configs != nil {
		if err := m.configs.ReserveDocs(ctx, doc.UserID, 1); err != nil {
			return nil, err
		}
		if err := m.configs.consumeInputs(doc.UserID, []domain.Document{*doc}); err != nil {
			_ = m.configs.RefundDocs(ctx, doc.UserID, 1)
			return nil, err
		}
	}
	created, err := m.Create(ctx, doc)
	if err != nil && m.configs != nil {
		_ = m.configs.RefundDocs(ctx, doc.UserID, 1)
	}
	return created, err
}
func (m *mockDocRepoAdapter) CreateManyWithQuota(ctx context.Context, userID int64, docs []domain.Document) ([]domain.Document, error) {
	if m.configs != nil {
		if err := m.configs.ReserveDocs(ctx, userID, int64(len(docs))); err != nil {
			return nil, err
		}
		if err := m.configs.consumeInputs(userID, docs); err != nil {
			_ = m.configs.RefundDocs(ctx, userID, int64(len(docs)))
			return nil, err
		}
	}
	created, err := m.CreateMany(ctx, docs)
	if err != nil && m.configs != nil {
		_ = m.configs.RefundDocs(ctx, userID, int64(len(docs)))
	}
	return created, err
}
func (m *mockDocRepoAdapter) CreateMany(ctx context.Context, docs []domain.Document) ([]domain.Document, error) {
	created := make([]domain.Document, 0, len(docs))
	for i := range docs {
		doc, err := m.Create(ctx, &docs[i])
		if err != nil {
			return nil, err
		}
		created = append(created, *doc)
	}
	return created, nil
}
func (m *mockDocRepoAdapter) GetByID(ctx context.Context, id string) (*domain.Document, error) {
	d, ok := m.docs[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return d, nil
}
func (m *mockDocRepoAdapter) ListByUser(ctx context.Context, u int64, s domain.DocumentStatus, l, o int) ([]domain.Document, error) {
	var res []domain.Document
	for _, d := range m.byUser[u] {
		res = append(res, *d)
	}
	return res, nil
}
func (m *mockDocRepoAdapter) UpdateStatus(ctx context.Context, id string, s domain.DocumentStatus, a, rK, rT, e string, expiresAt *time.Time) error {
	d, ok := m.docs[id]
	if !ok {
		return domain.ErrNotFound
	}
	d.Status = s
	d.ResultText = rT
	if a != "" {
		d.AttemptID = a
	}
	if expiresAt != nil {
		d.ResultExpiresAt = expiresAt
	}
	return nil
}
func (m *mockDocRepoAdapter) Cancel(ctx context.Context, id string, u int64) error {
	d, ok := m.docs[id]
	if !ok || d.UserID != u || d.Status != domain.StatusQueued {
		return domain.ErrConflict
	}
	d.Status = domain.StatusCancelled
	return nil
}
func (m *mockDocRepoAdapter) CancelWithRefund(ctx context.Context, id string, u int64, _ *domain.NotificationEvent) (*domain.Document, error) {
	if err := m.Cancel(ctx, id, u); err != nil {
		return nil, err
	}
	return m.docs[id], nil
}
func (m *mockDocRepoAdapter) ClaimNext(ctx context.Context, _ string, _ time.Duration, _ int) (*domain.Document, error) {
	return nil, nil
}
func (m *mockDocRepoAdapter) RequeueAttempt(context.Context, string, string) error { return nil }
func (m *mockDocRepoAdapter) ReleaseAttempt(context.Context, string, string) error { return nil }
func (m *mockDocRepoAdapter) ListExhaustedAttempts(context.Context, time.Time, int, int) ([]domain.Document, error) {
	return nil, nil
}
func (m *mockDocRepoAdapter) FinalizeAttempt(_ context.Context, f domain.DocumentFinalization, _ *domain.NotificationEvent) (*domain.Document, error) {
	d := m.docs[f.DocumentID]
	if d == nil {
		return nil, domain.ErrNotFound
	}
	d.Status, d.ResultKey, d.ResultText, d.ErrorDetail = f.Status, f.ResultKey, f.ResultText, f.ErrorDetail
	d.ResultExpiresAt = f.ResultExpiresAt
	return d, nil
}
func (m *mockDocRepoAdapter) CountByStatus(ctx context.Context, u *int64) (map[domain.DocumentStatus]int64, error) {
	return nil, nil
}
func (m *mockDocRepoAdapter) ListExpiredResults(context.Context, time.Time, int) ([]domain.Document, error) {
	return nil, nil
}
func (m *mockDocRepoAdapter) MarkResultExpired(context.Context, string) error { return nil }
func (m *mockDocRepoAdapter) ListExpiredInputs(context.Context, time.Time, int) ([]domain.Document, error) {
	return nil, nil
}
func (m *mockDocRepoAdapter) MarkInputExpired(context.Context, string) error { return nil }
func (m *mockDocRepoAdapter) ListExpiredDocuments(context.Context, time.Time, int) ([]domain.Document, error) {
	return nil, nil
}
func (m *mockDocRepoAdapter) DeleteExpiredDocument(context.Context, string, time.Time) error {
	return nil
}
func (m *mockDocRepoAdapter) IsInputKeyReferenced(_ context.Context, key string) (bool, error) {
	for _, doc := range m.docs {
		if doc.InputKey == key {
			return true, nil
		}
	}
	return false, nil
}
