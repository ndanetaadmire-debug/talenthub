package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ndanetaadmire-debug/talenthub/internal/ats"
)

func call(t *testing.T, h http.Handler, method, path, role string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("X-Role", role)
	req.Header.Set("X-User", role+"-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestHTTPFlow(t *testing.T) {
	h := New(ats.NewService(nil))
	job := map[string]any{"title": "Engineer", "pass_mark": 3, "criteria": []map[string]any{{"Key": "tech", "Label": "Tech", "Weight": 1}}}
	if code, _ := call(t, h, "POST", "/v1/jobs", "interviewer", job); code != 403 {
		t.Fatalf("only recruiters create jobs: %d", code)
	}
	code, j := call(t, h, "POST", "/v1/jobs", "recruiter", job)
	if code != 201 {
		t.Fatalf("create job: %d %v", code, j)
	}
	jobID := j["id"].(string)

	app := map[string]any{"identity": map[string]any{"FullName": "Ayanda Zulu", "Email": "ayanda@mail.com"}, "profile": map[string]any{"Skills": []string{"Go"}}, "consent": map[string]any{"Processing": true}}
	if code, _ := call(t, h, "POST", "/v1/jobs/"+jobID+"/applications", "", map[string]any{"identity": app["identity"], "consent": map[string]any{}}); code != 422 {
		t.Fatalf("consent required: %d", code)
	}
	code, c := call(t, h, "POST", "/v1/jobs/"+jobID+"/applications", "", app)
	if code != 201 {
		t.Fatalf("apply: %d %v", code, c)
	}
	candID := c["id"].(string)

	_, q := call(t, h, "GET", "/v1/jobs/"+jobID+"/review-queue", "interviewer", nil)
	first := q["data"].([]any)[0].(map[string]any)
	if _, leaked := first["name"]; leaked {
		t.Fatal("name leaked in blind screening")
	}
	if code, _ := call(t, h, "POST", "/v1/candidates/"+candID+"/stage", "recruiter", map[string]any{"to": "hired"}); code != 409 {
		t.Fatalf("invalid transition: %d", code)
	}
	if code, _ := call(t, h, "DELETE", "/v1/candidates/"+candID, "candidate", nil); code != 204 {
		t.Fatalf("erasure: %d", code)
	}
	if code, _ := call(t, h, "POST", "/v1/jobs", "recruiter", map[string]any{"bogus": 1}); code != 400 {
		t.Fatalf("unknown fields rejected: %d", code)
	}
}
