package plugin

type GatewayStageExecutionMode string

const (
	GatewayExecutionPipeline   GatewayStageExecutionMode = "pipeline"
	GatewayExecutionFirstMatch GatewayStageExecutionMode = "first_match"
	GatewayExecutionExclusive  GatewayStageExecutionMode = "exclusive"
	GatewayExecutionObserver   GatewayStageExecutionMode = "observer"
)

func GatewayStageExecutionModeFor(stage GatewayHookStage) (GatewayStageExecutionMode, bool) {
	switch stage {
	case StageDecodeNormalize, StageCacheLookup, StageProviderCall:
		return GatewayExecutionFirstMatch, true
	case StageRouteRank:
		return GatewayExecutionExclusive, true
	case StageSettlement, StageTraceExport:
		return GatewayExecutionObserver, true
	case StageAuthContext,
		StageAdmission,
		StagePrivacyPre,
		StageGuardrailPre,
		StageContextOptimize,
		StageRouteCandidates,
		StageRequestTransform,
		StageStreamTransform,
		StageResponsePost,
		StageGuardrailPost,
		StageUsageAttribution,
		StageCacheWrite:
		return GatewayExecutionPipeline, true
	default:
		return "", false
	}
}
