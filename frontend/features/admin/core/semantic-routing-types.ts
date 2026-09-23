export type SemanticRoutingCandidate = {
  id: string;
  provider_id: string;
  provider_model: string;
  criteria: string;
};

export type SemanticRoutingPolicy = {
  /** Server-managed; preserves continuation ownership after strategy changes. */
  response_binding_required?: boolean;
  mode: "off" | "shadow" | "enforce";
  min_confidence: number;
  instructions?: string;
  default_candidate_id?: string;
  candidates?: SemanticRoutingCandidate[];
};
