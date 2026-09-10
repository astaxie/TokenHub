package plugin

import "fmt"

// List stages have no selected route. Use project, key, endpoint, or operation
// scopes and inspect the full candidate list in the handler.
func validateGatewayListStageScope(hook GatewayHookDescriptor) error {
	if hook.Stage != StageRouteCandidates && hook.Stage != StageRouteRank {
		return nil
	}
	scope := hook.Scope
	if len(scope.ProviderIDs)+len(scope.ProviderTypes)+len(scope.ResourceIDs)+len(scope.ResourceTypes) != 0 {
		return fmt.Errorf("gateway hook stage %q cannot use provider or resource scopes before route selection", hook.Stage)
	}
	return nil
}
