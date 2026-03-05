package ats

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Service is safe for concurrent use. Storage is in-memory behind a mutex; the shape maps onto
// Postgres tables (jobs, candidates, candidate_identity, scorecards, events) one-to-one.
type Service struct {
	mu         sync.RWMutex
	jobs       map[string]*Job
	candidates map[string]*Candidate
	scorecards map[string][]Scorecard
	conflicts  map[string]map[string]bool // interviewer → declared candidate IDs
	now        func() time.Time
	retention  time.Duration // how long rejected candidates without talent-pool consent are kept
}

func NewService(now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{
		jobs: map[string]*Job{}, candidates: map[string]*Candidate{}, scorecards: map[string][]Scorecard{},
		conflicts: map[string]map[string]bool{}, now: now, retention: 180 * 24 * time.Hour,
	}
}

func newID(p string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return p + "_" + hex.EncodeToString(b[:])
}

func (s *Service) CreateJob(title string, criteria []Criterion, passMark float64) (*Job, error) {
	if strings.TrimSpace(title) == "" || len(criteria) == 0 || passMark < 1 || passMark > 5 {
		return nil, fmt.Errorf("%w: title, criteria and a pass mark between 1 and 5 are required", ErrInvalid)
	}
	var w float64
	for _, c := range criteria {
		if c.Key == "" || c.Weight <= 0 {
			return nil, fmt.Errorf("%w: every criterion needs a key and positive weight", ErrInvalid)
		}
		w += c.Weight
	}
	if math.Abs(w-1) > 1e-9 {
		return nil, fmt.Errorf("%w: criterion weights must sum to 1 (got %.2f)", ErrInvalid, w)
	}
	j := &Job{ID: newID("job"), Title: title, Criteria: criteria, PassMark: passMark, OpenedAt: s.now()}
	s.mu.Lock()
	s.jobs[j.ID] = j
	s.mu.Unlock()
	return j, nil
}

func (s *Service) Apply(jobID string, id Identity, p Profile, c Consent, demo *Demographics) (*Candidate, error) {
	if !c.Processing {
		return nil, fmt.Errorf("%w: processing consent is required to apply", ErrConsent)
	}
	if !strings.Contains(id.Email, "@") || strings.TrimSpace(id.FullName) == "" {
		return nil, fmt.Errorf("%w: name and email are required", ErrInvalid)
	}
	if demo != nil && !c.Demographics {
		demo = nil // never store demographic data without explicit consent
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[jobID]; !ok {
		return nil, ErrNotFound
	}
	for _, x := range s.candidates {
		if x.JobID == jobID && strings.EqualFold(x.Identity.Email, id.Email) && !x.Purged {
			return nil, fmt.Errorf("%w: already applied for this job", ErrInvalid)
		}
	}
	now := s.now()
	c.GivenAt = now
	cand := &Candidate{ID: newID("cand"), JobID: jobID, Identity: id, Profile: p, Stage: Applied, Consent: c, Demo: demo, Applied: now, Updated: now}
	s.candidates[cand.ID] = cand
	return cand, nil
}

func (s *Service) Move(candidateID string, to Stage, actor, reason string) (*Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.candidates[candidateID]
	if !ok || c.Purged {
		return nil, ErrNotFound
	}
	if !CanMove(c.Stage, to) {
		return nil, fmt.Errorf("%w: %s → %s", ErrTransition, c.Stage, to)
	}
	if to == Rejected && strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("%w: a job-related reason is required to reject", ErrInvalid)
	}
	if to == Offer {
		res, err := s.resultLocked(c)
		if err != nil {
			return nil, err
		}
		if !res.Passed {
			return nil, fmt.Errorf("%w: interview score %.2f is below the pass mark %.2f", ErrInvalid, res.Weighted, s.jobs[c.JobID].PassMark)
		}
	}
	c.History = append(c.History, Event{At: s.now(), Actor: actor, From: c.Stage, To: to, Reason: reason})
	c.Stage, c.Updated = to, s.now()
	return c, nil
}

// DeclareConflict records that an interviewer knows a candidate; they're then blocked from scoring them.
func (s *Service) DeclareConflict(interviewer, candidateID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conflicts[interviewer] == nil {
		s.conflicts[interviewer] = map[string]bool{}
	}
	s.conflicts[interviewer][candidateID] = true
}

// SubmitScorecard enforces structured interviewing: every criterion scored 1–5 with written evidence,
// one scorecard per interviewer, no scoring candidates you've declared a conflict with.
func (s *Service) SubmitScorecard(sc Scorecard) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.candidates[sc.CandidateID]
	if !ok || c.Purged {
		return ErrNotFound
	}
	if c.Stage != Interview {
		return fmt.Errorf("%w: scorecards are only accepted at the interview stage", ErrInvalid)
	}
	if s.conflicts[sc.Interviewer][sc.CandidateID] {
		return ErrConflict
	}
	for _, existing := range s.scorecards[sc.CandidateID] {
		if existing.Interviewer == sc.Interviewer {
			return fmt.Errorf("%w: interviewer already submitted a scorecard", ErrInvalid)
		}
	}
	for _, cr := range s.jobs[c.JobID].Criteria {
		v, ok := sc.Scores[cr.Key]
		if !ok || v < 1 || v > 5 {
			return fmt.Errorf("%w: criterion %q needs a score from 1 to 5", ErrInvalid, cr.Key)
		}
		if len(strings.TrimSpace(sc.Evidence[cr.Key])) < 20 {
			return fmt.Errorf("%w: criterion %q needs written evidence (≥ 20 characters)", ErrInvalid, cr.Key)
		}
	}
	sc.SubmittedAt = s.now()
	s.scorecards[sc.CandidateID] = append(s.scorecards[sc.CandidateID], sc)
	return nil
}

type Result struct {
	Weighted     float64
	Interviewers int
	Passed       bool
	Spread       map[string]int // max−min per criterion: large spreads signal a calibration discussion
}

func (s *Service) Result(candidateID string) (Result, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.candidates[candidateID]
	if !ok {
		return Result{}, ErrNotFound
	}
	return s.resultLocked(c)
}

func (s *Service) resultLocked(c *Candidate) (Result, error) {
	cards := s.scorecards[c.ID]
	job := s.jobs[c.JobID]
	if len(cards) < 2 {
		return Result{}, fmt.Errorf("%w: at least two independent scorecards are required", ErrInvalid)
	}
	res := Result{Interviewers: len(cards), Spread: map[string]int{}}
	for _, cr := range job.Criteria {
		lo, hi, sum := 5, 1, 0
		for _, sc := range cards {
			v := sc.Scores[cr.Key]
			sum += v
			lo, hi = min(lo, v), max(hi, v)
		}
		res.Weighted += cr.Weight * float64(sum) / float64(len(cards))
		res.Spread[cr.Key] = hi - lo
	}
	res.Weighted = math.Round(res.Weighted*100) / 100
	res.Passed = res.Weighted >= job.PassMark
	return res, nil
}

// ReviewView is what a reviewer sees. At applied/screening it is blind: no name, email, phone or photo.
type ReviewView struct {
	CandidateID string   `json:"candidate_id"`
	Stage       Stage    `json:"stage"`
	Name        string   `json:"name,omitempty"`
	Email       string   `json:"email,omitempty"`
	Skills      []string `json:"skills"`
	Years       int      `json:"years_experience"`
	Summary     string   `json:"summary"`
}

func (s *Service) ReviewQueue(jobID string) []ReviewView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ReviewView
	for _, c := range s.candidates {
		if c.JobID != jobID || c.Purged || c.Stage == Rejected || c.Stage == Withdrawn {
			continue
		}
		v := ReviewView{CandidateID: c.ID, Stage: c.Stage, Skills: c.Profile.Skills, Years: c.Profile.YearsExperience, Summary: redactSelfIdentifying(c.Profile.Summary, c.Identity)}
		if c.Stage != Applied && c.Stage != Screening {
			v.Name, v.Email = c.Identity.FullName, c.Identity.Email
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CandidateID < out[j].CandidateID })
	return out
}

// redactSelfIdentifying removes the candidate's own name from free text so a summary can't un-blind them.
func redactSelfIdentifying(text string, id Identity) string {
	for _, part := range strings.Fields(id.FullName) {
		if len(part) < 2 {
			continue
		}
		text = replaceFold(text, part, "[candidate]")
	}
	return text
}

func replaceFold(s, old, repl string) string {
	lower, lold := strings.ToLower(s), strings.ToLower(old)
	var b strings.Builder
	for {
		i := strings.Index(lower, lold)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i] + repl)
		s, lower = s[i+len(old):], lower[i+len(old):]
	}
}

// PurgeExpired implements POPIA data minimisation: candidates who were rejected or withdrew, and did not opt
// into the talent pool, have personal data erased after the retention period. Aggregate stats survive.
func (s *Service) PurgeExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.candidates {
		if c.Purged || c.Consent.TalentPool || (c.Stage != Rejected && c.Stage != Withdrawn) {
			continue
		}
		if s.now().Sub(c.Updated) >= s.retention {
			c.Identity, c.Profile, c.Purged = Identity{}, Profile{}, true
			delete(s.scorecards, c.ID)
			n++
		}
	}
	return n
}

// Erase handles a data-subject deletion request immediately, at any stage.
func (s *Service) Erase(candidateID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.candidates[candidateID]
	if !ok {
		return ErrNotFound
	}
	c.Identity, c.Profile, c.Demo, c.Purged = Identity{}, Profile{}, nil, true
	delete(s.scorecards, candidateID)
	return nil
}

type ImpactRow struct {
	Group       string  `json:"group"`
	Applicants  int     `json:"applicants"`
	Advanced    int     `json:"advanced"`
	Rate        float64 `json:"selection_rate"`
	ImpactRatio float64 `json:"impact_ratio"`
	Flag        bool    `json:"flag"`
}

// AdverseImpact applies the four-fifths rule to self-reported groups (consented data only): a group whose
// screening pass rate is under 80% of the highest group's rate is flagged for review. Groups smaller than
// minGroup are suppressed so individuals can't be re-identified from small counts.
func (s *Service) AdverseImpact(jobID string, minGroup int) []ImpactRow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	counts := map[string]*ImpactRow{}
	for _, c := range s.candidates {
		if c.JobID != jobID || c.Demo == nil || !c.Consent.Demographics {
			continue
		}
		r := counts[c.Demo.Group]
		if r == nil {
			r = &ImpactRow{Group: c.Demo.Group}
			counts[c.Demo.Group] = r
		}
		r.Applicants++
		if passedScreening(c) {
			r.Advanced++
		}
	}
	var rows []ImpactRow
	best := 0.0
	for _, r := range counts {
		if r.Applicants < minGroup {
			continue
		}
		r.Rate = float64(r.Advanced) / float64(r.Applicants)
		best = math.Max(best, r.Rate)
		rows = append(rows, *r)
	}
	for i := range rows {
		if best > 0 {
			rows[i].ImpactRatio = math.Round(rows[i].Rate/best*100) / 100
			rows[i].Flag = rows[i].ImpactRatio < 0.8
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Group < rows[j].Group })
	return rows
}

func passedScreening(c *Candidate) bool {
	for _, e := range c.History {
		if e.From == Screening && e.To == Interview {
			return true
		}
	}
	return false
}

func (s *Service) Candidate(id string) (*Candidate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.candidates[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *c
	return &cp, nil
}
