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

func TestChecksListEachOnce(t *testing.T) {
	v := Verification{
		ServerChecks: []ClientCheck{{Name: "truncation", Status: "warn", Detail: "server"}},
		ClientChecks: []ClientCheck{{Name: "truncation", Status: "warn", Detail: "client"}, {Name: "provenance", Status: "pass"}},
	}
	got := v.Checks()
	if len(got) != 2 || got[0].Detail != "server" || got[1].Name != "provenance" {
		t.Errorf("checks = %+v", got)
	}
}
