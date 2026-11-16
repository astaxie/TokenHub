package quotasource

import (
	"encoding/json"
	"sync"
)

// CompleteTencentPlanList follows Magpie's list-then-detail read. A failed
// detail keeps the list's values; each plan remains a separate window.
func CompleteTencentPlanList(body []byte, fetch func(string) ([]byte, error)) []byte {
	var list struct {
		Response *struct {
			TokenPlanSet []tencentPlan
			Error        any
		}
	}
	if json.Unmarshal(body, &list) != nil || list.Response == nil || list.Response.Error != nil || len(list.Response.TokenPlanSet) > 100 {
		return body
	}
	var wg sync.WaitGroup
	workers := make(chan struct{}, 4)
	for i, plan := range list.Response.TokenPlanSet {
		if plan.TeamID == "" {
			continue
		}
		workers <- struct{}{}
		wg.Go(func() {
			defer func() { <-workers }()
			raw, err := fetch(plan.TeamID)
			var detail struct {
				Response *struct {
					tencentPlan
					Error any
				}
			}
			if err != nil || json.Unmarshal(raw, &detail) != nil || detail.Response == nil || detail.Response.Error != nil {
				return
			}
			d := detail.Response
			if d.PackageInfo != nil {
				plan.PackageInfo = d.PackageInfo
			}
			if d.TokenSummary != nil {
				plan.TokenSummary = d.TokenSummary
			}
			if d.Status != "" {
				plan.Status, plan.StopReason = d.Status, d.StopReason
			}
			if d.ProductType != "" {
				plan.ProductType = d.ProductType
			}
			list.Response.TokenPlanSet[i] = plan
		})
	}
	wg.Wait()
	merged, err := json.Marshal(list)
	if err != nil {
		return body
	}
	return merged
}
