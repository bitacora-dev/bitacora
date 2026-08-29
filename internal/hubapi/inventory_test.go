package hubapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

type fakeInventories struct {
	byHostKind map[string]schema.Inventory
}

func (f *fakeInventories) GetInventory(ctx context.Context, hostID string, kind schema.InventoryKind) (schema.Inventory, bool, error) {
	inv, ok := f.byHostKind[hostID+"/"+string(kind)]
	return inv, ok, nil
}

func sampleShareInventory(hostID string) schema.Inventory {
	return schema.Inventory{
		HostID: hostID,
		Kind:   schema.InventoryShare,
		Schema: schema.CurrentSchemaVersion,
		Items: []schema.InventoryItem{
			{ID: "multimedia", Name: "Multimedia", Attrs: schema.Labels{"protocol": "smb"}},
		},
	}
}

func TestHandleInventory_ReturnsStoredSnapshot(t *testing.T) {
	inv := sampleShareInventory("host-a")
	srv := &Server{
		Metrics:     &fakeMetrics{},
		Events:      &fakeEvents{},
		Inventories: &fakeInventories{byHostKind: map[string]schema.Inventory{"host-a/share": inv}},
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/inventory?host_id=host-a&kind=share", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got schema.Inventory
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unexpected error decoding response: %v", err)
	}
	if got.HostID != "host-a" || got.Kind != schema.InventoryShare || len(got.Items) != 1 {
		t.Fatalf("unexpected inventory: %+v", got)
	}
}

func TestHandleInventory_UnknownHostKindReturns404(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{}, Events: &fakeEvents{}, Inventories: &fakeInventories{byHostKind: map[string]schema.Inventory{}}}

	req := httptest.NewRequest(http.MethodGet, "/v1/inventory?host_id=host-a&kind=share", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleInventory_MissingHostIDIsBadRequest(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{}, Events: &fakeEvents{}, Inventories: &fakeInventories{}}

	req := httptest.NewRequest(http.MethodGet, "/v1/inventory?kind=share", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleInventory_MissingKindIsBadRequest(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{}, Events: &fakeEvents{}, Inventories: &fakeInventories{}}

	req := httptest.NewRequest(http.MethodGet, "/v1/inventory?host_id=host-a", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleInventory_NilInventoriesReturns404(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{}, Events: &fakeEvents{}} // Inventories not set

	req := httptest.NewRequest(http.MethodGet, "/v1/inventory?host_id=host-a&kind=share", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when Inventories isn't configured, got %d", rec.Code)
	}
}

func TestHandleInventory_RejectsNonGET(t *testing.T) {
	srv := &Server{Metrics: &fakeMetrics{}, Events: &fakeEvents{}, Inventories: &fakeInventories{}}

	req := httptest.NewRequest(http.MethodPost, "/v1/inventory?host_id=host-a&kind=share", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandleInventory_RequiresDeviceTokenWhenDevicesConfigured(t *testing.T) {
	inv := sampleShareInventory("host-a")
	srv := &Server{
		Metrics:     &fakeMetrics{},
		Events:      &fakeEvents{},
		Inventories: &fakeInventories{byHostKind: map[string]schema.Inventory{"host-a/share": inv}},
		Devices:     NewDeviceTokenStore(),
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/inventory?host_id=host-a&kind=share", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without an Authorization header, got %d", rec.Code)
	}
}
