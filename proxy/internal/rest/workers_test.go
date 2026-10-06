package rest

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"macocr/proxy/internal/native"
	"net/http/httptest"
	"testing"
	"time"
)

type listingWorkerStore struct{}

func (listingWorkerStore) CreateEnrollment(context.Context, string, time.Time) error    { return nil }
func (listingWorkerStore) Enroll(context.Context, string, string, string, string) error { return nil }
func (listingWorkerStore) Secret(context.Context, string) (string, error)               { return "secret", nil }
func (listingWorkerStore) ListWorkers(context.Context) ([]native.WorkerNode, error) {
	return []native.WorkerNode{}, nil
}
func (listingWorkerStore) Revoke(context.Context, string) error             { return nil }
func (listingWorkerStore) Assign(context.Context, native.WorkerRoute) error { return nil }
func (listingWorkerStore) AssignedNode(context.Context, string, string) (string, error) {
	return "", nil
}
func (listingWorkerStore) Routes(context.Context) ([]native.WorkerRoute, error) {
	return []native.WorkerRoute{}, nil
}
func TestWorkerListReportsConfiguredAuthenticationMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"enrollment", "shared"} {
		t.Run(mode, func(t *testing.T) {
			store := listingWorkerStore{}
			hub := native.NewWorkerHub(store)
			if mode == "shared" {
				hub.SetSharedSecret("shared")
			}
			handler := NewWorkersHandler(hub, store)
			router := gin.New()
			router.GET("/workers", handler.List)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", "/workers", nil))
			var response struct {
				AuthMode string `json:"authMode"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.AuthMode != mode {
				t.Fatalf("mode=%s response=%s err=%v", mode, w.Body.String(), err)
			}
		})
	}
}
