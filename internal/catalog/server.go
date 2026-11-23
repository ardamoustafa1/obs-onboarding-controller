// Package catalog implements the service-catalog REST API: the portal's
// read model of "what services exist, who owns them, where's their
// dashboard". It is a real, runnable HTTP service (see cmd/catalog), not a
// mock -- the operator's internal/catalogclient talks to a running instance
// of exactly this server over HTTP.
//
// Storage is in-memory and process-local. That is a deliberate scope cut
// for this repo: a production deployment would back this with a real
// database (the handlers only depend on the Store interface, so swapping in
// a Postgres-backed Store does not change the HTTP layer). Losing the
// in-memory store on restart is acceptable because the operator's periodic
// reconcile (see internal/controller) re-upserts every ServiceObservability
// on its own drift-detection cadence, so the catalog self-heals within one
// reconcile interval.
package catalog

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/acme/obs-onboarding-controller/internal/catalogapi"
)

// Store is the persistence seam. The in-memory implementation below
// satisfies it; a durable backend can too.
type Store interface {
	Upsert(rec catalogapi.ServiceRecord)
	Get(environment, name string) (catalogapi.ServiceRecord, bool)
	List() []catalogapi.ServiceRecord
	Decommission(environment, name string) (catalogapi.ServiceRecord, bool)
}

type memStore struct {
	mu      sync.RWMutex
	records map[string]catalogapi.ServiceRecord
}

func NewMemStore() Store {
	return &memStore{records: make(map[string]catalogapi.ServiceRecord)}
}

func (s *memStore) Upsert(rec catalogapi.ServiceRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[rec.Key()] = rec
}

func (s *memStore) Get(environment, name string) (catalogapi.ServiceRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[environment+"/"+name]
	return rec, ok
}

func (s *memStore) List() []catalogapi.ServiceRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]catalogapi.ServiceRecord, 0, len(s.records))
	for _, rec := range s.records {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

// Decommission marks a record decommissioned rather than deleting it, so
// the catalog keeps an audit trail of every service that ever onboarded.
func (s *memStore) Decommission(environment, name string) (catalogapi.ServiceRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := environment + "/" + name
	rec, ok := s.records[key]
	if !ok {
		return catalogapi.ServiceRecord{}, false
	}
	rec.Status = catalogapi.StatusDecommissioned
	rec.UpdatedAt = time.Now().UTC()
	s.records[key] = rec
	return rec, true
}

type Server struct {
	store Store
}

func NewServer(store Store) *Server {
	return &Server{store: store}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/services", s.handleUpsert)
	mux.HandleFunc("GET /v1/services", s.handleList)
	mux.HandleFunc("GET /v1/services/{environment}/{name}", s.handleGet)
	mux.HandleFunc("POST /v1/services/{environment}/{name}/decommission", s.handleDecommission)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func (s *Server) handleUpsert(w http.ResponseWriter, r *http.Request) {
	var rec catalogapi.ServiceRecord
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if rec.Name == "" || rec.Environment == "" {
		writeError(w, http.StatusBadRequest, "name and environment are required")
		return
	}
	if rec.Status == "" {
		rec.Status = catalogapi.StatusActive
	}
	rec.UpdatedAt = time.Now().UTC()
	s.store.Upsert(rec)
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.List())
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.store.Get(r.PathValue("environment"), r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleDecommission(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.store.Decommission(r.PathValue("environment"), r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
