package api

import (
	"encoding/json"
	"testing"
)

func TestVerificationNamesPreferLabels(t *testing.T) {
	var v Verification
	json.Unmarshal([]byte(`{"key_id":"k1","reviewer_id":"k2","requester_label":"lab-mi300","reviewer_label":"launch80 review"}`), &v)
	if v.Requester() != "lab-mi300" || v.Reviewer() != "launch80 review" {
		t.Errorf("labels: %q %q", v.Requester(), v.Reviewer())
	}
	v = Verification{KeyID: "k1"}
	if v.Requester() != "k1" || v.Reviewer() != "" {
		t.Errorf("fallback: %q %q", v.Requester(), v.Reviewer())
	}
}
