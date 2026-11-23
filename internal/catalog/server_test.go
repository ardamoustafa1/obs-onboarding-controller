package catalog_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/acme/obs-onboarding-controller/internal/catalog"
	"github.com/acme/obs-onboarding-controller/internal/catalogapi"
)

func TestUpsertGetAndDecommission(t *testing.T) {
	srv := httptest.NewServer(catalog.NewServer(catalog.NewMemStore()).Handler())
	defer srv.Close()

	rec := catalogapi.ServiceRecord{Name: "checkout-api", Environment: "prod", Team: "team-checkout", ServiceType: "http"}
	body, _ := json.Marshal(rec)
	resp, err := http.Post(srv.URL+"/v1/services", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upsert: got %d", resp.StatusCode)
	}

	getResp, err := http.Get(srv.URL + "/v1/services/prod/checkout-api")
	if err != nil {
		t.Fatal(err)
	}
	var got catalogapi.ServiceRecord
	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Status != catalogapi.StatusActive {
		t.Fatalf("expected active status, got %q", got.Status)
	}

	decResp, err := http.Post(srv.URL+"/v1/services/prod/checkout-api/decommission", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if decResp.StatusCode != http.StatusOK {
		t.Fatalf("decommission: got %d", decResp.StatusCode)
	}

	getResp2, err := http.Get(srv.URL + "/v1/services/prod/checkout-api")
	if err != nil {
		t.Fatal(err)
	}
	var afterDecommission catalogapi.ServiceRecord
	if err := json.NewDecoder(getResp2.Body).Decode(&afterDecommission); err != nil {
		t.Fatal(err)
	}
	if afterDecommission.Status != catalogapi.StatusDecommissioned {
		t.Fatalf("expected decommissioned status, got %q", afterDecommission.Status)
	}
}

func TestDecommissionUnknownServiceReturns404(t *testing.T) {
	srv := httptest.NewServer(catalog.NewServer(catalog.NewMemStore()).Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/services/prod/does-not-exist/decommission", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
