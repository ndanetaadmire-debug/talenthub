# TalentHub

[![CI](https://github.com/ndanetaadmire-debug/talenthub/actions/workflows/ci.yml/badge.svg)](https://github.com/ndanetaadmire-debug/talenthub/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go&logoColor=white)
![Dependencies](https://img.shields.io/badge/runtime%20deps-0-brightgreen)
![POPIA](https://img.shields.io/badge/POPIA-retention%20%26%20erasure-0a7)

**An applicant-tracking backend built for fair, auditable hiring.** Most ATS products optimise for speed through the funnel. TalentHub also builds in the practices that make hiring defensible and fairer, and enforces them in code so they can't be skipped on a busy week.

## What it enforces

| Practice | How it's enforced |
|---|---|
| **Blind first-round review** | Identity is stored separately from the evaluable profile. Reviewers at *applied* and *screening* see skills, experience and a summary with the candidate's own name redacted, never name, email, phone or photo. Identity appears from the interview stage. |
| **Structured interviews** | Each job defines weighted, job-related criteria *before* interviewing (weights must sum to 1). Every scorecard must score every criterion 1–5 **with written evidence**. One scorecard per interviewer. |
| **Independent judgement** | Offers need **at least two scorecards**; the result reports per-criterion **spread**, so large disagreements prompt a calibration conversation instead of being averaged away. |
| **Conflicts of interest** | Interviewers who declare they know a candidate are blocked from scoring them. |
| **Defensible decisions** | Pipeline transitions follow a state machine; rejections require a job-related reason; every move is recorded with actor and time. An offer below the job's pass mark is refused. |
| **Fairness monitoring** | The **four-fifths rule**: screening pass rates per self-reported group, flagged when a group's rate is below 80% of the highest. Uses consented data only, and **suppresses small groups** so nobody can be re-identified. |
| **POPIA consent** | Processing consent is required to apply; talent-pool and demographic consent are separate opt-ins. Demographic data is **discarded** if submitted without consent. |
| **Data minimisation** | A retention sweep erases personal data of rejected or withdrawn candidates after 180 days unless they opted into the talent pool; stage history survives for aggregate statistics. |
| **Right to erasure** | `DELETE /v1/candidates/{id}` erases personal data immediately, at any stage. |

## API

| Method | Path | Role |
|---|---|---|
| `POST` | `/v1/jobs` | recruiter |
| `POST` | `/v1/jobs/{id}/applications` | public |
| `GET` | `/v1/jobs/{id}/review-queue` | recruiter, interviewer (blind at screening) |
| `GET` | `/v1/jobs/{id}/adverse-impact` | recruiter |
| `POST` | `/v1/candidates/{id}/stage` | recruiter |
| `POST` | `/v1/candidates/{id}/scorecards` | interviewer |
| `GET` | `/v1/candidates/{id}/result` | recruiter |
| `DELETE` | `/v1/candidates/{id}` | recruiter, candidate |

Roles come from the identity provider's group claim in production; the demo reads an `X-Role` header.

## Design

- **Go standard library only**: `net/http` 1.22 routing, `log/slog`, strict JSON decoding, graceful shutdown, distroless image.
- The domain (`internal/ats`) is pure Go with an injected clock, so retention and timing rules are tested deterministically.
- In-memory storage behind a mutex, shaped one-to-one onto Postgres tables (`candidates`, `candidate_identity`, `scorecards`, `events`). Keeping identity in its own table means blind review and erasure stay simple in SQL too.

## Tests

Weights validation, consent and deduplication, demographics discarded without consent, blind review including name redaction in free text, transition rules, mandatory evidence, conflict blocking, two-scorecard rule, weighted scoring and spread, the pass-mark offer gate, four-fifths flagging with small-group suppression, retention timing, talent-pool exemption and immediate erasure. Everything runs under the race detector.

```bash
go test -race -cover ./...
go run ./cmd/talenthub
```

---

Built by **[Admire T. Ndaneta](https://admire-portfolio.vercel.app)**. MIT licensed.
