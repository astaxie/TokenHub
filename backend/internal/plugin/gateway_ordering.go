package plugin

import (
	"fmt"
	"sort"
	"strings"
)

func orderAndValidateGatewayHooks(hooks []GatewayHookDescriptor) ([]GatewayHookDescriptor, error) {
	if len(hooks) < 2 {
		return hooks, nil
	}
	stage := hooks[0].Stage
	byKey := make(map[string]int, len(hooks))
	byHookID := make(map[string][]int, len(hooks))
	v2Count := 0
	for index, hook := range hooks {
		if hook.Stage != stage {
			return nil, fmt.Errorf("gateway hook ordering requires a single stage")
		}
		key := gatewayHookOrderingKey(hook)
		if _, exists := byKey[key]; exists {
			return nil, fmt.Errorf("duplicate gateway hook %s", key)
		}
		byKey[key] = index
		byHookID[hook.HookID] = append(byHookID[hook.HookID], index)
		if hook.PluginAPI == PluginAPIV2 {
			v2Count++
		}
	}
	mode, _ := GatewayStageExecutionModeFor(stage)
	if mode == GatewayExecutionExclusive && v2Count > 1 {
		return nil, fmt.Errorf("gateway stage %q is exclusive and accepts only one plugin API v2 hook", stage)
	}

	edges := make([]map[int]struct{}, len(hooks))
	indegree := make([]int, len(hooks))
	for index := range edges {
		edges[index] = map[int]struct{}{}
	}
	addEdge := func(from int, to int) {
		if from == to {
			return
		}
		if _, exists := edges[from][to]; exists {
			return
		}
		edges[from][to] = struct{}{}
		indegree[to]++
	}
	for index, hook := range hooks {
		for _, reference := range hook.Before {
			if target, ok := resolveGatewayHookReference(reference, byKey, byHookID); ok {
				addEdge(index, target)
			}
		}
		for _, reference := range hook.After {
			if target, ok := resolveGatewayHookReference(reference, byKey, byHookID); ok {
				addEdge(target, index)
			}
		}
	}
	if mode == GatewayExecutionPipeline {
		if err := validateGatewayWriteOrdering(hooks, edges); err != nil {
			return nil, err
		}
	}

	ready := make([]int, 0, len(hooks))
	for index, degree := range indegree {
		if degree == 0 {
			ready = append(ready, index)
		}
	}
	sortGatewayHookIndexes(ready, hooks)
	ordered := make([]GatewayHookDescriptor, 0, len(hooks))
	for len(ready) > 0 {
		current := ready[0]
		ready = ready[1:]
		ordered = append(ordered, hooks[current])
		for target := range edges[current] {
			indegree[target]--
			if indegree[target] == 0 {
				ready = append(ready, target)
				sortGatewayHookIndexes(ready, hooks)
			}
		}
	}
	if len(ordered) != len(hooks) {
		return nil, fmt.Errorf("gateway hook before/after dependencies contain a cycle at stage %q", stage)
	}
	return ordered, nil
}

func validateGatewayWriteOrdering(hooks []GatewayHookDescriptor, edges []map[int]struct{}) error {
	for left := 0; left < len(hooks); left++ {
		if hooks[left].PluginAPI != PluginAPIV2 {
			continue
		}
		for right := left + 1; right < len(hooks); right++ {
			if hooks[right].PluginAPI != PluginAPIV2 || !gatewayHooksShareWrite(hooks[left], hooks[right]) {
				continue
			}
			if !gatewayHookReachable(left, right, edges, map[int]bool{}) && !gatewayHookReachable(right, left, edges, map[int]bool{}) {
				return fmt.Errorf("gateway hooks %s and %s write the same data class without before/after ordering", gatewayHookOrderingKey(hooks[left]), gatewayHookOrderingKey(hooks[right]))
			}
		}
	}
	return nil
}

func gatewayHooksShareWrite(left GatewayHookDescriptor, right GatewayHookDescriptor) bool {
	writes := map[GatewayDataClass]struct{}{}
	for _, dataClass := range left.Writes {
		writes[dataClass] = struct{}{}
	}
	for _, dataClass := range right.Writes {
		if _, exists := writes[dataClass]; exists {
			return true
		}
	}
	return false
}

func gatewayHookReachable(from int, target int, edges []map[int]struct{}, seen map[int]bool) bool {
	if from == target {
		return true
	}
	if seen[from] {
		return false
	}
	seen[from] = true
	for next := range edges[from] {
		if gatewayHookReachable(next, target, edges, seen) {
			return true
		}
	}
	return false
}

func resolveGatewayHookReference(reference string, byKey map[string]int, byHookID map[string][]int) (int, bool) {
	reference = strings.TrimSpace(reference)
	if index, ok := byKey[reference]; ok {
		return index, true
	}
	matches := byHookID[reference]
	returnIndex := 0
	if len(matches) == 1 {
		returnIndex = matches[0]
		return returnIndex, true
	}
	return 0, false
}

func gatewayHookOrderingKey(hook GatewayHookDescriptor) string {
	return strings.TrimSpace(hook.PluginID) + "/" + strings.TrimSpace(hook.HookID)
}

func sortGatewayHookIndexes(indexes []int, hooks []GatewayHookDescriptor) {
	sort.Slice(indexes, func(i, j int) bool {
		left := hooks[indexes[i]]
		right := hooks[indexes[j]]
		if left.PluginAPI != PluginAPIV2 && right.PluginAPI != PluginAPIV2 && left.Priority != right.Priority {
			return left.Priority < right.Priority
		}
		return gatewayHookOrderingKey(left) < gatewayHookOrderingKey(right)
	})
}
