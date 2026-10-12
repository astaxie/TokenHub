package tokenhubplugin

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRetrievalEvidenceRetainsExplicitZero(t *testing.T) {
	zero := int64(0)
	encoded, err := json.Marshal(Usage{RetrievalEvidence: &RetrievalEvidence{Unit: "search_unit", Quantity: &zero, Source: "upstream"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"quantity":0`) {
		t.Fatalf("measured zero lost: %s", encoded)
	}
	var decoded Usage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.RetrievalEvidence == nil || decoded.RetrievalEvidence.Quantity == nil {
		t.Fatal("native evidence lost on round trip")
	}
	missing, err := json.Marshal(Usage{RetrievalEvidence: &RetrievalEvidence{Unit: "token", Source: "unreported"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(missing), `"quantity"`) {
		t.Fatal("unreported quantity became zero")
	}
}
