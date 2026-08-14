package tests

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"macocr/proxy/domain"
	"macocr/proxy/internal/usecase/object"
)

// mockRepo is a minimal in-memory ObjectRepository for unit tests.
type mockRepo struct {
	store   map[string][]byte
	pingErr error
}

func newMockRepo() *mockRepo {
	return &mockRepo{store: map[string][]byte{}}
}

func (m *mockRepo) Put(_ context.Context, key string, body io.Reader, _ string) error {
	if m.pingErr != nil {
		return m.pingErr
	}
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.store[key] = b
	return nil
}

func (m *mockRepo) Get(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := m.store[key]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *mockRepo) Exists(_ context.Context, key string) (bool, error) {
	if m.pingErr != nil {
		return false, m.pingErr
	}
	_, ok := m.store[key]
	return ok, nil
}

func (m *mockRepo) PresignGetURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "http://example.test/" + key, nil
}

func (m *mockRepo) Ping(_ context.Context) error { return m.pingErr }
func (m *mockRepo) Delete(_ context.Context, key string) error {
	delete(m.store, key)
	return nil
}

func TestPutAndGet(t *testing.T) {
	svc := object.NewService(newMockRepo())
	ctx := context.Background()

	key := "uploads/u1/o1"
	if err := svc.Put(ctx, key, bytes.NewBufferString("hello"), "text/plain"); err != nil {
		t.Fatalf("put: %v", err)
	}

	exists, err := svc.Exists(ctx, key)
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if !exists {
		t.Fatal("expected object to exist")
	}

	rc, err := svc.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "hello" {
		t.Fatalf("got %q want %q", got, "hello")
	}
}

func TestGetMissingReturnsNotFound(t *testing.T) {
	svc := object.NewService(newMockRepo())
	_, err := svc.Get(context.Background(), "missing")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestReadyPropagatesStorageError(t *testing.T) {
	repo := newMockRepo()
	repo.pingErr = domain.ErrStorageUnavailable
	svc := object.NewService(repo)

	err := svc.Ready(context.Background())
	if !errors.Is(err, domain.ErrStorageUnavailable) {
		t.Fatalf("want ErrStorageUnavailable, got %v", err)
	}
}

func TestPresignGetURL(t *testing.T) {
	svc := object.NewService(newMockRepo())
	url, err := svc.PresignGetURL(context.Background(), "k", time.Minute)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	if url != "http://example.test/k" {
		t.Fatalf("unexpected url %q", url)
	}
}
