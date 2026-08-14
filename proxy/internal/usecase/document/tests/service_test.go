package tests

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"macocr/proxy/domain"
	"macocr/proxy/internal/usecase/auth"
	"macocr/proxy/internal/usecase/document"
)

var validPNGBytes, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")

type mockDocRepo struct {
	docs          map[string]*domain.Document
	byUser        map[int64][]*domain.Document
	createManyErr error
	claimedIdx    int
	configs       *mockConfigRepo
}

func (m *mockDocRepo) CreateWithQuota(ctx context.Context, doc *domain.Document) (*domain.Document, error) {
	if m.configs != nil {
		if err := m.configs.ReserveDocs(ctx, doc.UserID, 1); err != nil {
			return nil, err
		}
	}
	return m.Create(ctx, doc)
}

func (m *mockDocRepo) CreateManyWithQuota(ctx context.Context, userID int64, docs []domain.Document) ([]domain.Document, error) {
	if m.createManyErr != nil {
		return nil, m.createManyErr
	}
	if m.configs != nil {
		if err := m.configs.ReserveDocs(ctx, userID, int64(len(docs))); err != nil {
			return nil, err
		}
	}
	return m.CreateMany(ctx, docs)
}

func newMockDocRepo() *mockDocRepo {
	return &mockDocRepo{
		docs:   make(map[string]*domain.Document),
		byUser: make(map[int64][]*domain.Document),
	}
}

func (m *mockDocRepo) Create(ctx context.Context, doc *domain.Document) (*domain.Document, error) {
	d := *doc
	d.CreatedAt = time.Now()
	d.UpdatedAt = time.Now()
	m.docs[d.ID] = &d
	m.byUser[d.UserID] = append(m.byUser[d.UserID], &d)
	return &d, nil
}

func (m *mockDocRepo) CreateMany(ctx context.Context, docs []domain.Document) ([]domain.Document, error) {
	if m.createManyErr != nil {
		return nil, m.createManyErr
	}
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

func (m *mockDocRepo) GetByID(ctx context.Context, id string) (*domain.Document, error) {
	d, ok := m.docs[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return d, nil
}

func (m *mockDocRepo) ListByUser(ctx context.Context, userID int64, status domain.DocumentStatus, limit, offset int) ([]domain.Document, error) {
	list := m.byUser[userID]
	if userID == 0 {
		list = nil
		for _, userDocs := range m.byUser {
			list = append(list, userDocs...)
		}
	}
	res := []domain.Document{}
	for _, d := range list {
		if status == "" || d.Status == status {
			res = append(res, *d)
		}
	}
	return res, nil
}

func (m *mockDocRepo) UpdateStatus(ctx context.Context, id string, status domain.DocumentStatus, attemptID, resultKey, resultText, errorDetail string, expiresAt *time.Time) error {
	d, ok := m.docs[id]
	if !ok {
		return domain.ErrNotFound
	}
	d.Status = status
	if attemptID != "" {
		d.AttemptID = attemptID
	}
	if resultKey != "" {
		d.ResultKey = resultKey
	}
	if resultText != "" {
		d.ResultText = resultText
	}
	if errorDetail != "" {
		d.ErrorDetail = errorDetail
	}
	d.UpdatedAt = time.Now()
	d.ResultExpiresAt = expiresAt
	return nil
}

func (m *mockDocRepo) Cancel(ctx context.Context, id string, userID int64) error {
	d, ok := m.docs[id]
	if !ok || d.UserID != userID || d.Status != domain.StatusQueued {
		return domain.ErrConflict
	}
	d.Status = domain.StatusCancelled
	return nil
}

func (m *mockDocRepo) CancelWithRefund(ctx context.Context, id string, userID int64, _ *domain.NotificationEvent) (*domain.Document, error) {
	if err := m.Cancel(ctx, id, userID); err != nil {
		return nil, err
	}
	if m.configs != nil {
		_ = m.configs.RefundDocs(ctx, userID, 1)
	}
	return m.docs[id], nil
}

func (m *mockDocRepo) ClaimNext(ctx context.Context, attemptID string, lease time.Duration, _ int) (*domain.Document, error) {
	for _, d := range m.docs {
		if d.Status == domain.StatusQueued {
			d.Status = domain.StatusProcessing
			d.AttemptID = attemptID
			d.AttemptCount++
			until := time.Now().Add(lease)
			d.ProcessingUntil = &until
			return d, nil
		}
	}
	return nil, nil
}
func (m *mockDocRepo) RequeueAttempt(_ context.Context, id, attemptID string) error {
	d := m.docs[id]
	if d == nil || d.AttemptID != attemptID || d.Status != domain.StatusProcessing {
		return domain.ErrConflict
	}
	d.Status, d.AttemptID, d.ProcessingUntil = domain.StatusQueued, "", nil
	return nil
}
func (m *mockDocRepo) ReleaseAttempt(ctx context.Context, id, attemptID string) error {
	if err := m.RequeueAttempt(ctx, id, attemptID); err != nil {
		return err
	}
	if m.docs[id].AttemptCount > 0 {
		m.docs[id].AttemptCount--
	}
	return nil
}
func (m *mockDocRepo) ListExhaustedAttempts(context.Context, time.Time, int, int) ([]domain.Document, error) {
	return nil, nil
}
func (m *mockDocRepo) FinalizeAttempt(_ context.Context, f domain.DocumentFinalization, _ *domain.NotificationEvent) (*domain.Document, error) {
	d := m.docs[f.DocumentID]
	if d == nil || d.Status != domain.StatusProcessing || d.AttemptID != f.AttemptID {
		return nil, domain.ErrConflict
	}
	d.Status, d.TerminalEventID, d.ResultKey, d.ResultText, d.ErrorDetail = f.Status, f.TerminalEventID, f.ResultKey, f.ResultText, f.ErrorDetail
	d.ResultExpiresAt, d.ProcessingUntil = f.ResultExpiresAt, nil
	if f.RefundQuota && m.configs != nil {
		_ = m.configs.RefundDocs(context.Background(), d.UserID, 1)
	}
	return d, nil
}

func (m *mockDocRepo) CountByStatus(ctx context.Context, userID *int64) (map[domain.DocumentStatus]int64, error) {
	counts := make(map[domain.DocumentStatus]int64)
	for _, d := range m.docs {
		if userID == nil || d.UserID == *userID {
			counts[d.Status]++
		}
	}
	return counts, nil
}
func (m *mockDocRepo) ListExpiredResults(context.Context, time.Time, int) ([]domain.Document, error) {
	return nil, nil
}
func (m *mockDocRepo) MarkResultExpired(context.Context, string) error { return nil }
func (m *mockDocRepo) ListExpiredInputs(context.Context, time.Time, int) ([]domain.Document, error) {
	return nil, nil
}
func (m *mockDocRepo) MarkInputExpired(context.Context, string) error { return nil }
func (m *mockDocRepo) ListExpiredDocuments(context.Context, time.Time, int) ([]domain.Document, error) {
	return nil, nil
}
func (m *mockDocRepo) DeleteExpiredDocument(context.Context, string, time.Time) error {
	return nil
}
func (m *mockDocRepo) IsInputKeyReferenced(_ context.Context, key string) (bool, error) {
	for _, doc := range m.docs {
		if doc.InputKey == key {
			return true, nil
		}
	}
	return false, nil
}

func TestSubmitBatchRefundsQuotaWhenAtomicPersistenceFails(t *testing.T) {
	ctx := context.Background()
	docRepo := newMockDocRepo()
	docRepo.createManyErr = errors.New("database unavailable")
	objRepo := newMockObjectRepo()
	userRepo := newMockUserRepo()
	cfgRepo := newMockConfigRepo()
	docRepo.configs = cfgRepo
	userRepo.users[1] = &domain.User{ID: 1, Email: "user@test.com"}
	cfgRepo.configs[1] = &domain.AccountConfig{UserID: 1, RateLimitRPM: 60, DocQuota: 5}

	svc := document.NewService(docRepo, objRepo, auth.NewService(userRepo, cfgRepo, &mockKeyRepo{}, nil), nil, nil)
	b64PNG := base64.StdEncoding.EncodeToString(validPNGBytes)
	docs, err := svc.SubmitBatch(ctx, 1, []document.BatchItemInput{
		{Input: document.InputSource{Type: "base64", Base64Data: b64PNG}},
		{Input: document.InputSource{Type: "base64", Base64Data: b64PNG}},
	})
	if err == nil {
		t.Fatal("expected persistence error")
	}
	if len(docs) != 0 || len(docRepo.docs) != 0 {
		t.Fatalf("atomic failure exposed documents: returned=%d stored=%d", len(docs), len(docRepo.docs))
	}
	if cfgRepo.configs[1].DocUsed != 0 {
		t.Fatalf("expected quota refund, doc_used=%d", cfgRepo.configs[1].DocUsed)
	}
	if len(objRepo.stored) != 0 {
		t.Fatalf("failed batch left %d orphaned input objects", len(objRepo.stored))
	}
}

type mockUserRepo struct {
	users map[int64]*domain.User
}

func newMockUserRepo() *mockUserRepo {
	return &mockUserRepo{users: make(map[int64]*domain.User)}
}
func (m *mockUserRepo) Create(ctx context.Context, u *domain.User) (*domain.User, error) {
	m.users[u.ID] = u
	return u, nil
}
func (m *mockUserRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return u, nil
}
func (m *mockUserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return nil, domain.ErrNotFound
}
func (m *mockUserRepo) List(ctx context.Context, limit, offset int) ([]domain.User, error) {
	return nil, nil
}
func (m *mockUserRepo) Update(ctx context.Context, u *domain.User) (*domain.User, error) {
	m.users[u.ID] = u
	return u, nil
}

type mockConfigRepo struct {
	configs map[int64]*domain.AccountConfig
}

func newMockConfigRepo() *mockConfigRepo {
	return &mockConfigRepo{configs: make(map[int64]*domain.AccountConfig)}
}
func (m *mockConfigRepo) GetByUserID(ctx context.Context, userID int64) (*domain.AccountConfig, error) {
	cfg, ok := m.configs[userID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return cfg, nil
}
func (m *mockConfigRepo) Update(ctx context.Context, cfg *domain.AccountConfig) (*domain.AccountConfig, error) {
	m.configs[cfg.UserID] = cfg
	return cfg, nil
}
func (m *mockConfigRepo) ReserveDocs(ctx context.Context, userID int64, count int64) error {
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
func (m *mockConfigRepo) RefundDocs(ctx context.Context, userID int64, count int64) error {
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
func (m *mockConfigRepo) ResetDocUsed(ctx context.Context, userID int64) error {
	if cfg, ok := m.configs[userID]; ok {
		cfg.DocUsed = 0
	}
	return nil
}

type mockKeyRepo struct{}

func (m *mockKeyRepo) Create(ctx context.Context, k *domain.ApiKey) (*domain.ApiKey, error) {
	return k, nil
}
func (m *mockKeyRepo) ListByUser(ctx context.Context, userID int64) ([]domain.ApiKey, error) {
	return nil, nil
}
func (m *mockKeyRepo) GetByHash(ctx context.Context, hash string) (*domain.ApiKey, error) {
	return nil, domain.ErrNotFound
}
func (m *mockKeyRepo) Revoke(ctx context.Context, id int64) error { return nil }

func TestDocumentService_SubmitSingleAndBatch(t *testing.T) {
	ctx := context.Background()
	docRepo := newMockDocRepo()
	objRepo := newMockObjectRepo()
	userRepo := newMockUserRepo()
	cfgRepo := newMockConfigRepo()
	docRepo.configs = cfgRepo
	keyRepo := &mockKeyRepo{}

	userRepo.users[1] = &domain.User{ID: 1, Email: "user@test.com"}
	cfgRepo.configs[1] = &domain.AccountConfig{UserID: 1, RateLimitRPM: 60, DocQuota: 5, DocUsed: 0}

	authSvc := auth.NewService(userRepo, cfgRepo, keyRepo, nil)
	svc := document.NewService(docRepo, objRepo, authSvc, nil, nil)

	pngData := validPNGBytes
	b64Png := base64.StdEncoding.EncodeToString(pngData)
	doc1, err := svc.SubmitSingle(ctx, 1, document.InputSource{Type: "base64", Base64Data: b64Png}, nil, nil)
	if err != nil {
		t.Fatalf("SubmitSingle failed: %v", err)
	}

	if doc1.Status != domain.StatusQueued {
		t.Errorf("expected queued, got %s", doc1.Status)
	}
	if cfgRepo.configs[1].DocUsed != 1 {
		t.Errorf("expected doc_used=1, got %d", cfgRepo.configs[1].DocUsed)
	}

	batchItems := []document.BatchItemInput{
		{Input: document.InputSource{Type: "base64", Base64Data: b64Png}},
		{Input: document.InputSource{Type: "base64", Base64Data: b64Png}},
	}
	docs, err := svc.SubmitBatch(ctx, 1, batchItems)
	if err != nil {
		t.Fatalf("SubmitBatch failed: %v", err)
	}
	if len(docs) != 2 {
		t.Errorf("expected 2 docs returned, got %d", len(docs))
	}
	if cfgRepo.configs[1].DocUsed != 3 {
		t.Errorf("expected doc_used=3, got %d", cfgRepo.configs[1].DocUsed)
	}

	cfgRepo.configs[1].DocUsed = 5
	_, err = svc.SubmitSingle(ctx, 1, document.InputSource{Type: "base64", Base64Data: b64Png}, nil, nil)
	if err != domain.ErrQuotaExceeded {
		t.Errorf("expected ErrQuotaExceeded, got %v", err)
	}
}

func TestGetDocumentReturnsGoneAfterResultTTL(t *testing.T) {
	docRepo := newMockDocRepo()
	expired := time.Now().Add(-time.Minute)
	docRepo.docs["doc_expired"] = &domain.Document{ID: "doc_expired", UserID: 7, Status: domain.StatusCompleted, ResultText: "must not leak", ResultExpiresAt: &expired}
	svc := document.NewService(docRepo, newMockObjectRepo(), nil, nil, nil)
	_, err := svc.GetDocument(context.Background(), 7, "doc_expired")
	if err != domain.ErrResultExpired {
		t.Fatalf("expected ErrResultExpired, got %v", err)
	}
}

type mockResultCache struct {
	results map[string]*domain.OCRResult
}

func (m *mockResultCache) SetResult(_ context.Context, id string, result *domain.OCRResult, _ time.Duration) error {
	m.results[id] = result
	return nil
}
func (m *mockResultCache) GetResult(_ context.Context, id string) (*domain.OCRResult, error) {
	result, ok := m.results[id]
	if !ok {
		return nil, domain.ErrResultExpired
	}
	return result, nil
}
func (m *mockResultCache) DeleteResult(_ context.Context, id string) error {
	delete(m.results, id)
	return nil
}

func TestGetDocumentReadsCompletedPayloadFromResultCache(t *testing.T) {
	docRepo := newMockDocRepo()
	expires := time.Now().Add(time.Hour)
	docRepo.docs["doc_cached"] = &domain.Document{ID: "doc_cached", UserID: 7, Status: domain.StatusCompleted, ResultText: "stale database value", ResultExpiresAt: &expires}
	cache := &mockResultCache{results: map[string]*domain.OCRResult{"doc_cached": {Text: "redis value", PageCount: 2}}}
	svc := document.NewService(docRepo, newMockObjectRepo(), nil, nil, cache)
	doc, err := svc.GetDocument(context.Background(), 7, "doc_cached")
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if doc.Result == nil || doc.Result.Text != "redis value" || doc.Result.PageCount != 2 {
		t.Fatalf("result was not loaded from cache: %+v", doc.Result)
	}
}

func TestListDocumentsAdminReturnsDocumentsAcrossAccounts(t *testing.T) {
	docRepo := newMockDocRepo()
	_, _ = docRepo.Create(context.Background(), &domain.Document{ID: "doc_user_1", UserID: 1, Status: domain.StatusQueued})
	_, _ = docRepo.Create(context.Background(), &domain.Document{ID: "doc_user_2", UserID: 2, Status: domain.StatusCompleted})
	svc := document.NewService(docRepo, newMockObjectRepo(), nil, nil, nil)

	docs, err := svc.ListDocumentsAdmin(context.Background(), "", 100, 0)
	if err != nil || len(docs) != 2 {
		t.Fatalf("admin list should include every account: docs=%d err=%v", len(docs), err)
	}
}
