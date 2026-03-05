package ats

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var criteria = []Criterion{{"technical", "Technical depth", 0.5}, {"communication", "Communication", 0.3}, {"ownership", "Ownership", 0.2}}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func setup(t *testing.T) (*Service, *Job, *clock) {
	t.Helper()
	c := &clock{time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)}
	s := NewService(c.now)
	j, err := s.CreateJob("Senior Backend Engineer", criteria, 3.5)
	if err != nil {
		t.Fatal(err)
	}
	return s, j, c
}

func applicant(s *Service, j *Job, name string, consent Consent, demo *Demographics) *Candidate {
	c, err := s.Apply(j.ID, Identity{FullName: name, Email: strings.ToLower(strings.ReplaceAll(name, " ", ".")) + "@mail.com"},
		Profile{Skills: []string{"Go", "PostgreSQL"}, YearsExperience: 6, Summary: "I am " + name + ", a backend engineer."}, consent, demo)
	if err != nil {
		panic(err)
	}
	return c
}

var consent = Consent{Processing: true}

func evidence() map[string]string {
	return map[string]string{
		"technical":     "Designed an idempotent payments API and explained trade-offs clearly.",
		"communication": "Structured answers, checked understanding, concise and clear.",
		"ownership":     "Described leading an incident review and following up on actions.",
	}
}

func TestJobValidation(t *testing.T) {
	s, _, _ := setup(t)
	if _, err := s.CreateJob("X", []Criterion{{"a", "A", 0.6}, {"b", "B", 0.6}}, 3); !errors.Is(err, ErrInvalid) {
		t.Fatalf("weights must sum to 1: %v", err)
	}
	if _, err := s.CreateJob("X", criteria, 6); !errors.Is(err, ErrInvalid) {
		t.Fatalf("pass mark range: %v", err)
	}
}

func TestApplicationRequiresConsentAndIsDeduplicated(t *testing.T) {
	s, j, _ := setup(t)
	if _, err := s.Apply(j.ID, Identity{FullName: "A B", Email: "a@b.com"}, Profile{}, Consent{}, nil); !errors.Is(err, ErrConsent) {
		t.Fatalf("consent: %v", err)
	}
	applicant(s, j, "Thandi Mokoena", consent, nil)
	if _, err := s.Apply(j.ID, Identity{FullName: "Thandi M", Email: "THANDI.MOKOENA@mail.com"}, Profile{}, consent, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate application should be rejected: %v", err)
	}
}

func TestDemographicsNeverStoredWithoutConsent(t *testing.T) {
	s, j, _ := setup(t)
	c := applicant(s, j, "Sipho Nkosi", consent, &Demographics{Group: "B"})
	if c.Demo != nil {
		t.Fatal("demographics stored without consent")
	}
}

func TestBlindScreeningHidesIdentityIncludingInFreeText(t *testing.T) {
	s, j, _ := setup(t)
	c := applicant(s, j, "Lerato Khumalo", consent, nil)
	q := s.ReviewQueue(j.ID)
	if q[0].Name != "" || q[0].Email != "" || strings.Contains(q[0].Summary, "Lerato") || strings.Contains(q[0].Summary, "Khumalo") {
		t.Fatalf("identity leaked at screening: %+v", q[0])
	}
	if !strings.Contains(q[0].Summary, "[candidate]") {
		t.Fatalf("summary should be redacted, got %q", q[0].Summary)
	}
	mustMove(t, s, c.ID, Screening)
	mustMove(t, s, c.ID, Interview)
	if s.ReviewQueue(j.ID)[0].Name != "Lerato Khumalo" {
		t.Fatal("identity should be revealed from interview stage")
	}
}

func mustMove(t *testing.T, s *Service, id string, to Stage) {
	t.Helper()
	if _, err := s.Move(id, to, "recruiter", "meets criteria"); err != nil {
		t.Fatal(err)
	}
}

func TestPipelineTransitions(t *testing.T) {
	s, j, _ := setup(t)
	c := applicant(s, j, "A B", consent, nil)
	if _, err := s.Move(c.ID, Offer, "r", ""); !errors.Is(err, ErrTransition) {
		t.Fatalf("cannot jump to offer: %v", err)
	}
	if _, err := s.Move(c.ID, Rejected, "r", " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("rejection needs a reason: %v", err)
	}
	mustMove(t, s, c.ID, Rejected)
	if _, err := s.Move(c.ID, Screening, "r", ""); !errors.Is(err, ErrTransition) {
		t.Fatalf("rejected is terminal: %v", err)
	}
}

func TestStructuredScorecardsAndOfferGate(t *testing.T) {
	s, j, _ := setup(t)
	c := applicant(s, j, "Naledi Dube", consent, nil)
	if err := s.SubmitScorecard(Scorecard{CandidateID: c.ID, Interviewer: "i1"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("scorecards only at interview stage")
	}
	mustMove(t, s, c.ID, Screening)
	mustMove(t, s, c.ID, Interview)

	if err := s.SubmitScorecard(Scorecard{CandidateID: c.ID, Interviewer: "i1", Scores: map[string]int{"technical": 5, "communication": 4, "ownership": 4},
		Evidence: map[string]string{"technical": "great", "communication": "x", "ownership": "y"}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("evidence is mandatory")
	}
	s.DeclareConflict("friend", c.ID)
	if err := s.SubmitScorecard(Scorecard{CandidateID: c.ID, Interviewer: "friend", Scores: map[string]int{"technical": 5, "communication": 5, "ownership": 5}, Evidence: evidence()}); !errors.Is(err, ErrConflict) {
		t.Fatal("conflicted interviewer must be blocked")
	}

	ok := func(who string, tech, comm, own int) {
		if err := s.SubmitScorecard(Scorecard{CandidateID: c.ID, Interviewer: who, Scores: map[string]int{"technical": tech, "communication": comm, "ownership": own}, Evidence: evidence()}); err != nil {
			t.Fatal(err)
		}
	}
	ok("i1", 4, 3, 4)
	if _, err := s.Move(c.ID, Offer, "r", ""); err == nil {
		t.Fatal("offer requires two independent scorecards")
	}
	ok("i2", 2, 3, 4)
	if err := s.SubmitScorecard(Scorecard{CandidateID: c.ID, Interviewer: "i2", Scores: map[string]int{"technical": 5, "communication": 5, "ownership": 5}, Evidence: evidence()}); err == nil {
		t.Fatal("one scorecard per interviewer")
	}
	res, _ := s.Result(c.ID)
	// technical 3.0*0.5 + communication 3.0*0.3 + ownership 4.0*0.2 = 3.2
	if res.Weighted != 3.2 || res.Passed || res.Spread["technical"] != 2 {
		t.Fatalf("unexpected result %+v", res)
	}
	if _, err := s.Move(c.ID, Offer, "r", ""); err == nil || !strings.Contains(err.Error(), "below the pass mark") {
		t.Fatalf("offer must be blocked below pass mark: %v", err)
	}
}

func TestAdverseImpactFourFifthsRuleWithSmallGroupSuppression(t *testing.T) {
	s, j, _ := setup(t)
	withDemo := Consent{Processing: true, Demographics: true}
	advance := func(group string, n, pass int) {
		for i := 0; i < n; i++ {
			c := applicant(s, j, fmt.Sprintf("%s Person%d", group, i), withDemo, &Demographics{Group: group})
			mustMove(t, s, c.ID, Screening)
			if i < pass {
				mustMove(t, s, c.ID, Interview)
			}
		}
	}
	advance("A", 10, 6) // 60%
	advance("B", 10, 3) // 30% → ratio 0.5, flagged
	advance("C", 3, 0)  // too small to report
	rows := s.AdverseImpact(j.ID, 5)
	if len(rows) != 2 || rows[0].Group != "A" || rows[0].Flag || !rows[1].Flag || rows[1].ImpactRatio != 0.5 {
		t.Fatalf("unexpected impact rows %+v", rows)
	}
}

func TestPopiaRetentionAndErasure(t *testing.T) {
	s, j, clk := setup(t)
	keep := applicant(s, j, "Talent Pool", Consent{Processing: true, TalentPool: true}, nil)
	gone := applicant(s, j, "Rejected Person", consent, nil)
	active := applicant(s, j, "Active Person", consent, nil)
	mustMove(t, s, keep.ID, Rejected)
	mustMove(t, s, gone.ID, Rejected)

	clk.t = clk.t.Add(100 * 24 * time.Hour)
	if n := s.PurgeExpired(); n != 0 {
		t.Fatalf("purged too early: %d", n)
	}
	clk.t = clk.t.Add(100 * 24 * time.Hour)
	if n := s.PurgeExpired(); n != 1 {
		t.Fatalf("expected 1 purge, got %d", n)
	}
	g, _ := s.Candidate(gone.ID)
	if !g.Purged || g.Identity.Email != "" || g.Stage != Rejected {
		t.Fatalf("personal data must be erased but stage kept for stats: %+v", g)
	}
	k, _ := s.Candidate(keep.ID)
	if k.Purged {
		t.Fatal("talent-pool consent must be respected")
	}
	if err := s.Erase(active.ID); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Candidate(active.ID); a.Identity.FullName != "" {
		t.Fatal("erasure request must take effect immediately")
	}
}
