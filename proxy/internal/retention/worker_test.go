package retention

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"macocr/proxy/domain"
)

type retentionDocs struct {
	expiredDocuments []domain.Document
	references       map[string]bool
	deleted          []string
}

func (r *retentionDocs) ListExpiredResults(context.Context, time.Time, int) ([]domain.Document, error) {
	return nil, nil
}
func (r *retentionDocs) MarkResultExpired(context.Context, string) error { return nil }
func (r *retentionDocs) ListExpiredInputs(context.Context, time.Time, int) ([]domain.Document, error) {
	return nil, nil
}
func (r *retentionDocs) MarkInputExpired(context.Context, string) error { return nil }
func (r *retentionDocs) ListExpiredDocuments(context.Context, time.Time, int) ([]domain.Document, error) {
	return r.expiredDocuments, nil
}
func (r *retentionDocs) DeleteExpiredDocument(_ context.Context, id string, _ time.Time) error {
	r.deleted = append(r.deleted, id)
	return nil
}
func (r *retentionDocs) IsInputKeyReferenced(_ context.Context, key string) (bool, error) {
	return r.references[key], nil
}

type retentionObjects struct {
	uploads []domain.ObjectInfo
	deleted []string
}

func (r *retentionObjects) Put(context.Context, string, io.Reader, string) error { return nil }
func (r *retentionObjects) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
func (r *retentionObjects) Exists(context.Context, string) (bool, error) { return true, nil }
func (r *retentionObjects) PresignGetURL(context.Context, string, time.Duration) (string, error) {
	return "https://storage.test/object", nil
}
func (r *retentionObjects) Delete(_ context.Context, key string) error {
	r.deleted = append(r.deleted, key)
	return nil
}
func (r *retentionObjects) Ping(context.Context) error { return nil }
func (r *retentionObjects) ListExpiredUploads(context.Context, time.Time, int) ([]domain.ObjectInfo, error) {
	return r.uploads, nil
}

func TestCleanupDeletesTerminalMetadataAndOnlyOrphanUploads(t *testing.T) {
	docs := &retentionDocs{
		expiredDocuments: []domain.Document{{ID: "doc_expired", InputKey: "inputs/expired", ResultKey: "results/expired"}},
		references:       map[string]bool{"uploads/1/referenced": true},
	}
	objects := &retentionObjects{uploads: []domain.ObjectInfo{
		{Key: "uploads/1/orphan"},
		{Key: "uploads/1/referenced"},
	}}
	worker := New(docs, nil, objects, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Hour, time.Hour, time.Hour, time.Hour)
	worker.cleanup(context.Background())

	if len(docs.deleted) != 1 || docs.deleted[0] != "doc_expired" {
		t.Fatalf("expired document metadata deletion = %v", docs.deleted)
	}
	wantDeleted := map[string]bool{"inputs/expired": true, "results/expired": true, "uploads/1/orphan": true}
	for _, key := range objects.deleted {
		delete(wantDeleted, key)
		if key == "uploads/1/referenced" {
			t.Fatal("referenced upload was deleted")
		}
	}
	if len(wantDeleted) != 0 {
		t.Fatalf("expected object deletions were not performed: %v; got %v", wantDeleted, objects.deleted)
	}
}
