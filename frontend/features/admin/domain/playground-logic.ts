export type PlaygroundBranchTurn = {
  id: string;
  selectedCandidateID: string;
};

export type PlaygroundUsageMetrics = {
  prompt_tokens?: number;
  cached_input_tokens?: number;
  completion_tokens?: number;
  total_tokens?: number;
  estimated_cost_usd?: number;
};

export function playgroundMaxTokenLimit(maxOutputTokens: unknown, contextWindow: unknown) {
  const declared = Number(maxOutputTokens);
  const context = Number(contextWindow);
  const candidate = Number.isFinite(declared) && declared > 0
    ? declared
    : Number.isFinite(context) && context > 0
      ? context
      : 32768;
  return Math.max(1, Math.min(Math.floor(candidate), 200000));
}

export function clampPlaygroundMaxTokens(value: number | undefined, limit: number) {
  const fallback = Math.min(4096, limit);
  const candidate = value !== undefined && Number.isFinite(value) ? Math.round(value) : fallback;
  return Math.max(1, Math.min(candidate, limit));
}

export function hasPlaygroundUsage(usage?: PlaygroundUsageMetrics) {
  if (!usage) return false;
  return [
    usage.prompt_tokens,
    usage.cached_input_tokens,
    usage.completion_tokens,
    usage.total_tokens,
    usage.estimated_cost_usd,
  ].some((value) => typeof value === "number" && Number.isFinite(value) && value > 0);
}

export function selectPlaygroundCandidateBranch<T extends PlaygroundBranchTurn>(turns: T[], turnID: string, candidateID: string) {
  const turnIndex = turns.findIndex((turn) => turn.id === turnID);
  if (turnIndex < 0 || turns[turnIndex].selectedCandidateID === candidateID) return turns;
  return turns.slice(0, turnIndex + 1).map((turn, index) => index === turnIndex
    ? { ...turn, selectedCandidateID: candidateID }
    : turn);
}
