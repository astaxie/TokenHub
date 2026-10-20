package server

import "testing"

func TestReviewedOCRCatalogOffersRetainTheirOperation(t *testing.T) {
	entries, err := loadLocalProviderCatalog("../../../data/provider-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for providerID, modelID := range map[string]string{"mistral": "mistral-ocr-4-1", "cohere": "parse-v5.0", "nvidia": "nvidia/nemotron-parse-2.0"} {
		found := false
		for _, entry := range entries {
			if entry.ID != providerID {
				continue
			}
			for _, model := range entry.Models {
				if model.ID == modelID {
					found = true
					if normalizeModelModality(model.Type) != "ocr" || model.Metadata["call_support"] != "unsupported" {
						t.Errorf("%s/%s lost its catalog-only OCR contract: %+v", providerID, modelID, model)
					}
				}
			}
		}
		if !found {
			t.Errorf("missing reviewed OCR offer %s/%s", providerID, modelID)
		}
	}
}
