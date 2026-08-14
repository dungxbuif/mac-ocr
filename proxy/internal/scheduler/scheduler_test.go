package scheduler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"macocr/proxy/domain"
	"macocr/proxy/internal/native"
	"macocr/proxy/internal/usecase/auth"
)

type schedulerDocRepo struct {
	doc                    *domain.Document
	requeued               int
	released               int
	weightedCapacityClaims int
}

func (r *schedulerDocRepo) ClaimNextWithinCapacity(ctx context.Context, attemptID string, lease time.Duration, maxAttempts, availableUnits, imageJobUnits, pdfJobUnits int) (*domain.Document, error) {
	r.weightedCapacityClaims++
	if r.doc == nil {
		return nil, nil
	}
	required := imageJobUnits
	if r.doc.InputContentType == "application/pdf" {
		required = pdfJobUnits
	}
	if required > availableUnits {
		return nil, nil
	}
	return r.ClaimNext(ctx, attemptID, lease, maxAttempts)
}

func (r *schedulerDocRepo) Create(context.Context, *domain.Document) (*domain.Document, error) {
	return nil, nil
}
func (r *schedulerDocRepo) CreateMany(context.Context, []domain.Document) ([]domain.Document, error) {
	return nil, nil
}
func (r *schedulerDocRepo) CreateWithQuota(context.Context, *domain.Document) (*domain.Document, error) {
	return nil, nil
}
func (r *schedulerDocRepo) CreateManyWithQuota(context.Context, int64, []domain.Document) ([]domain.Document, error) {
	return nil, nil
}
func (r *schedulerDocRepo) GetByID(context.Context, string) (*domain.Document, error) {
	return r.doc, nil
}
func (r *schedulerDocRepo) ListByUser(context.Context, int64, domain.DocumentStatus, int, int) ([]domain.Document, error) {
	return nil, nil
}
func (r *schedulerDocRepo) UpdateStatus(context.Context, string, domain.DocumentStatus, string, string, string, string, *time.Time) error {
	return nil
}
func (r *schedulerDocRepo) Cancel(context.Context, string, int64) error { return nil }
func (r *schedulerDocRepo) CancelWithRefund(context.Context, string, int64, *domain.NotificationEvent) (*domain.Document, error) {
	return nil, nil
}
func (r *schedulerDocRepo) ClaimNext(_ context.Context, attemptID string, lease time.Duration, _ int) (*domain.Document, error) {
	if r.doc == nil || r.doc.Status != domain.StatusQueued {
		return nil, nil
	}
	r.doc.Status, r.doc.AttemptID = domain.StatusProcessing, attemptID
	r.doc.AttemptCount++
	until := time.Now().Add(lease)
	r.doc.ProcessingUntil = &until
	return r.doc, nil
}
func (r *schedulerDocRepo) RequeueAttempt(context.Context, string, string) error {
	r.requeued++
	r.doc.Status, r.doc.AttemptID, r.doc.ProcessingUntil = domain.StatusQueued, "", nil
	return nil
}
func (r *schedulerDocRepo) ReleaseAttempt(context.Context, string, string) error {
	r.released++
	r.doc.Status, r.doc.AttemptID, r.doc.ProcessingUntil = domain.StatusQueued, "", nil
	r.doc.AttemptCount--
	return nil
}
func (r *schedulerDocRepo) ListExhaustedAttempts(context.Context, time.Time, int, int) ([]domain.Document, error) {
	return nil, nil
}
func (r *schedulerDocRepo) FinalizeAttempt(context.Context, domain.DocumentFinalization, *domain.NotificationEvent) (*domain.Document, error) {
	return r.doc, nil
}
func (r *schedulerDocRepo) CountByStatus(context.Context, *int64) (map[domain.DocumentStatus]int64, error) {
	return nil, nil
}
func (r *schedulerDocRepo) ListExpiredResults(context.Context, time.Time, int) ([]domain.Document, error) {
	return nil, nil
}
func (r *schedulerDocRepo) MarkResultExpired(context.Context, string) error { return nil }
func (r *schedulerDocRepo) ListExpiredInputs(context.Context, time.Time, int) ([]domain.Document, error) {
	return nil, nil
}
func (r *schedulerDocRepo) MarkInputExpired(context.Context, string) error { return nil }
func (r *schedulerDocRepo) ListExpiredDocuments(context.Context, time.Time, int) ([]domain.Document, error) {
	return nil, nil
}
func (r *schedulerDocRepo) DeleteExpiredDocument(context.Context, string, time.Time) error {
	return nil
}
func (r *schedulerDocRepo) IsInputKeyReferenced(_ context.Context, key string) (bool, error) {
	return r.doc != nil && r.doc.InputKey == key, nil
}

type schedulerObjects struct{ url string }

type schedulerRoundTrip func(*http.Request) (*http.Response, error)

func (fn schedulerRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func (o schedulerObjects) Put(context.Context, string, io.Reader, string) error { return nil }
func (o schedulerObjects) Get(context.Context, string) (io.ReadCloser, error)   { return nil, nil }
func (o schedulerObjects) Exists(context.Context, string) (bool, error)         { return true, nil }
func (o schedulerObjects) PresignGetURL(context.Context, string, time.Duration) (string, error) {
	return o.url, nil
}
func (o schedulerObjects) Delete(context.Context, string) error { return nil }
func (o schedulerObjects) Ping(context.Context) error           { return nil }

func TestBusyWorkerReleasesAttemptWithoutConsumingRetry(t *testing.T) {
	httpClient := &http.Client{Transport: schedulerRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/capacity" {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"state":"ready","effectiveLimit":1,"available":1}`)), Request: request}, nil
		}
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"busy"}`)), Request: request}, nil
	})}
	repo := &schedulerDocRepo{doc: &domain.Document{ID: "doc_test", UserID: 1, Status: domain.StatusQueued, InputKey: "input"}}
	s := New(repo, schedulerObjects{url: "https://storage.test/input"}, native.NewClientWithHTTPClient("http://worker.test", "secret", httpClient), auth.NewService(nil, nil, nil, nil), "https://proxy.test/webhooks/native/events", slog.New(slog.NewTextHandler(io.Discard, nil)), time.Minute, 3)
	s.pollAndDispatch(context.Background())
	if repo.released != 1 || repo.requeued != 0 || repo.doc.AttemptCount != 0 || repo.doc.Status != domain.StatusQueued {
		t.Fatalf("busy dispatch did not release cleanly: %+v requeued=%d released=%d", repo.doc, repo.requeued, repo.released)
	}
}

func TestInvalidWorkerAcceptanceKeepsAttemptLeased(t *testing.T) {
	httpClient := &http.Client{Transport: schedulerRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/capacity" {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"state":"ready","effectiveLimit":1,"available":1}`)), Request: request}, nil
		}
		return &http.Response{StatusCode: http.StatusAccepted, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"attemptId":"wrong","status":"accepted"}`)), Request: request}, nil
	})}
	repo := &schedulerDocRepo{doc: &domain.Document{ID: "doc_test", UserID: 1, Status: domain.StatusQueued, InputKey: "input", InputSHA256: strings.Repeat("a", 64)}}
	s := New(repo, schedulerObjects{url: "https://storage.test/input"}, native.NewClientWithHTTPClient("http://worker.test", "secret", httpClient), auth.NewService(nil, nil, nil, nil), "https://proxy.test/webhooks/native/events", slog.New(slog.NewTextHandler(io.Discard, nil)), time.Minute, 3)
	s.pollAndDispatch(context.Background())
	if repo.requeued != 0 || repo.released != 0 || repo.doc.AttemptCount != 1 || repo.doc.Status != domain.StatusProcessing || repo.doc.ProcessingUntil == nil {
		t.Fatalf("ambiguous acceptance was not kept leased: %+v requeued=%d released=%d", repo.doc, repo.requeued, repo.released)
	}
}

func TestZeroNativeCapacityDoesNotClaimDocument(t *testing.T) {
	httpClient := &http.Client{Transport: schedulerRoundTrip(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"state":"busy","effectiveLimit":3,"available":0}`)), Request: request}, nil
	})}
	repo := &schedulerDocRepo{doc: &domain.Document{ID: "doc_test", UserID: 1, Status: domain.StatusQueued, InputKey: "input"}}
	s := New(repo, schedulerObjects{url: "https://storage.test/input"}, native.NewClientWithHTTPClient("http://worker.test", "secret", httpClient), auth.NewService(nil, nil, nil, nil), "https://proxy.test/webhooks/native/events", slog.New(slog.NewTextHandler(io.Discard, nil)), time.Minute, 3)
	s.pollAndDispatch(context.Background())
	if repo.doc.Status != domain.StatusQueued || repo.doc.AttemptCount != 0 {
		t.Fatalf("scheduler claimed work with zero native capacity: %+v", repo.doc)
	}
}

func TestWeightedCapacitySkipsPDFThatDoesNotFit(t *testing.T) {
	httpClient := &http.Client{Transport: schedulerRoundTrip(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"state":"ready","effectiveLimit":1,"available":1,"availableUnits":1,"imageJobUnits":1,"pdfJobUnits":3}`)), Request: request}, nil
	})}
	repo := &schedulerDocRepo{doc: &domain.Document{ID: "doc_pdf", UserID: 1, Status: domain.StatusQueued, InputKey: "input", InputContentType: "application/pdf"}}
	s := New(repo, schedulerObjects{url: "https://storage.test/input"}, native.NewClientWithHTTPClient("http://worker.test", "secret", httpClient), auth.NewService(nil, nil, nil, nil), "https://proxy.test/webhooks/native/events", slog.New(slog.NewTextHandler(io.Discard, nil)), time.Minute, 3)
	s.pollAndDispatch(context.Background())
	if repo.weightedCapacityClaims != 1 || repo.doc.Status != domain.StatusQueued || repo.doc.AttemptCount != 0 {
		t.Fatalf("scheduler claimed an oversized PDF for available capacity: %+v weightedClaims=%d", repo.doc, repo.weightedCapacityClaims)
	}
}
