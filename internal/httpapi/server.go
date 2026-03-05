// Package httpapi exposes the ATS over JSON/HTTP with role-based access.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ndanetaadmire-debug/talenthub/internal/ats"
)

// Role comes from the identity provider in production (OIDC group claim); a header keeps the demo simple.
type Role string

const (
	Recruiter   Role = "recruiter"
	Interviewer Role = "interviewer"
	Candidate   Role = "candidate"
)

type Server struct{ svc *ats.Service }

func New(svc *ats.Service) http.Handler {
	s := &Server{svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { write(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /v1/jobs", s.role(s.createJob, Recruiter))
	mux.HandleFunc("POST /v1/jobs/{id}/applications", s.apply) // public
	mux.HandleFunc("GET /v1/jobs/{id}/review-queue", s.role(s.queue, Recruiter, Interviewer))
	mux.HandleFunc("GET /v1/jobs/{id}/adverse-impact", s.role(s.impact, Recruiter))
	mux.HandleFunc("POST /v1/candidates/{id}/stage", s.role(s.move, Recruiter))
	mux.HandleFunc("POST /v1/candidates/{id}/scorecards", s.role(s.scorecard, Interviewer))
	mux.HandleFunc("GET /v1/candidates/{id}/result", s.role(s.result, Recruiter))
	mux.HandleFunc("DELETE /v1/candidates/{id}", s.role(s.erase, Recruiter, Candidate))
	return mux
}

func (s *Server) role(h http.HandlerFunc, allowed ...Role) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := Role(r.Header.Get("X-Role"))
		for _, a := range allowed {
			if got == a {
				h(w, r)
				return
			}
		}
		fail(w, ats.ErrForbidden)
	}
}

func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Title    string          `json:"title"`
		Criteria []ats.Criterion `json:"criteria"`
		PassMark float64         `json:"pass_mark"`
	}
	if !decode(w, r, &b) {
		return
	}
	j, err := s.svc.CreateJob(b.Title, b.Criteria, b.PassMark)
	if err != nil {
		fail(w, err)
		return
	}
	write(w, 201, map[string]string{"id": j.ID})
}

func (s *Server) apply(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Identity ats.Identity      `json:"identity"`
		Profile  ats.Profile       `json:"profile"`
		Consent  ats.Consent       `json:"consent"`
		Demo     *ats.Demographics `json:"demographics"`
	}
	if !decode(w, r, &b) {
		return
	}
	c, err := s.svc.Apply(r.PathValue("id"), b.Identity, b.Profile, b.Consent, b.Demo)
	if err != nil {
		fail(w, err)
		return
	}
	write(w, 201, map[string]string{"id": c.ID})
}

func (s *Server) queue(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"data": s.svc.ReviewQueue(r.PathValue("id"))})
}

func (s *Server) impact(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"data": s.svc.AdverseImpact(r.PathValue("id"), 5)})
}

func (s *Server) move(w http.ResponseWriter, r *http.Request) {
	var b struct {
		To     ats.Stage `json:"to"`
		Reason string    `json:"reason"`
	}
	if !decode(w, r, &b) {
		return
	}
	c, err := s.svc.Move(r.PathValue("id"), b.To, r.Header.Get("X-User"), b.Reason)
	if err != nil {
		fail(w, err)
		return
	}
	write(w, 200, map[string]any{"id": c.ID, "stage": c.Stage})
}

func (s *Server) scorecard(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Scores   map[string]int    `json:"scores"`
		Evidence map[string]string `json:"evidence"`
	}
	if !decode(w, r, &b) {
		return
	}
	err := s.svc.SubmitScorecard(ats.Scorecard{CandidateID: r.PathValue("id"), Interviewer: r.Header.Get("X-User"), Scores: b.Scores, Evidence: b.Evidence})
	if err != nil {
		fail(w, err)
		return
	}
	write(w, 201, map[string]string{"status": "recorded"})
}

func (s *Server) result(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.Result(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	write(w, 200, res)
}

func (s *Server) erase(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Erase(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		write(w, 400, map[string]string{"error": "malformed JSON"})
		return false
	}
	return true
}

func fail(w http.ResponseWriter, err error) {
	code := 500
	switch {
	case errors.Is(err, ats.ErrNotFound):
		code = 404
	case errors.Is(err, ats.ErrForbidden), errors.Is(err, ats.ErrConflict):
		code = 403
	case errors.Is(err, ats.ErrTransition):
		code = 409
	case errors.Is(err, ats.ErrInvalid), errors.Is(err, ats.ErrConsent):
		code = 422
	}
	msg := err.Error()
	if code == 500 {
		msg = "internal error"
	}
	write(w, code, map[string]string{"error": strings.TrimSpace(msg)})
}

func write(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
