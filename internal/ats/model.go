// Package ats implements a recruitment pipeline designed for fair, auditable, POPIA-compliant hiring.
package ats

import (
	"errors"
	"time"
)

type Stage string

const (
	Applied   Stage = "applied"
	Screening Stage = "screening" // blind: reviewers see skills and experience, not identity
	Interview Stage = "interview"
	Offer     Stage = "offer"
	Hired     Stage = "hired"
	Rejected  Stage = "rejected"
	Withdrawn Stage = "withdrawn"
)

// Allowed transitions. Anything else is rejected, so the pipeline history is always meaningful.
var transitions = map[Stage][]Stage{
	Applied:   {Screening, Rejected, Withdrawn},
	Screening: {Interview, Rejected, Withdrawn},
	Interview: {Offer, Rejected, Withdrawn},
	Offer:     {Hired, Rejected, Withdrawn},
}

func CanMove(from, to Stage) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

type Job struct {
	ID       string
	Title    string
	Criteria []Criterion // structured, job-related, agreed before anyone is interviewed
	PassMark float64     // weighted average (1–5) needed to advance from interview
	OpenedAt time.Time
}

type Criterion struct {
	Key    string
	Label  string
	Weight float64
}

// Candidate holds identity separately from the evaluable profile so blind review is structural, not a UI trick.
type Candidate struct {
	ID       string
	JobID    string
	Identity Identity
	Profile  Profile
	Stage    Stage
	Consent  Consent
	Demo     *Demographics // optional, self-reported, only used in aggregate
	History  []Event
	Applied  time.Time
	Updated  time.Time
	Purged   bool
}

type Identity struct {
	FullName string
	Email    string
	Phone    string
	PhotoURL string
}

type Profile struct {
	Skills          []string
	YearsExperience int
	Summary         string
}

type Consent struct {
	Processing   bool // required to apply (POPIA lawful basis)
	TalentPool   bool // keep my details for future roles
	Demographics bool // use self-reported data for fairness monitoring
	GivenAt      time.Time
}

// Demographics are self-reported, optional and never shown to reviewers.
type Demographics struct {
	Group string
}

type Event struct {
	At     time.Time
	Actor  string
	From   Stage
	To     Stage
	Reason string
}

type Scorecard struct {
	CandidateID string
	Interviewer string
	Scores      map[string]int // criterion key → 1..5
	Evidence    map[string]string
	SubmittedAt time.Time
}

var (
	ErrNotFound   = errors.New("not found")
	ErrInvalid    = errors.New("invalid")
	ErrTransition = errors.New("transition not allowed")
	ErrConflict   = errors.New("conflict of interest")
	ErrConsent    = errors.New("consent required")
	ErrForbidden  = errors.New("forbidden")
)
