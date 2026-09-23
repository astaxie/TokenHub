package guardrails

import (
	"context"
	"errors"
	"regexp"
	"regexp/syntax"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	ActionAllow = "allow"
	// Work is estimated as input bytes multiplied by matcher complexity. The
	// aggregate budget permits 32 simple full-text scans over a 4 MiB prompt,
	// while the per-matcher budget rejects one pathological regex on long text.
	maxDeterministicMatcherWork     = 64 << 20
	maxDeterministicEvaluationWork  = 128 << 20
	maxDeterministicFindings        = 10_000
	regexpInstructionsPerWorkFactor = 8
)

var (
	ErrModelUnavailable                = errors.New("guardrail model detector is unavailable")
	ErrDeterministicWorkBudgetExceeded = errors.New("guardrail deterministic work budget exceeded")
)

type Fragment struct {
	ID      string
	Text    string
	Mutable bool
}

type EvaluationRequest struct {
	ProjectID      string
	Checkpoint     string
	Protocol       string
	Fragments      []Fragment
	Policies       []Policy
	IgnoreBindings bool
}

type Finding struct {
	PolicyID     string `json:"policy_id"`
	PolicyName   string `json:"policy_name"`
	ItemID       string `json:"detection_item_id"`
	ItemName     string `json:"detection_item_name"`
	DetectorType string `json:"detector_type"`
	Action       string `json:"action"`
	Category     string `json:"category"`
	ReasonCode   string `json:"reason_code"`
	FragmentID   string `json:"-"`
	Start        int    `json:"-"`
	End          int    `json:"-"`
}

type Decision struct {
	Action            string            `json:"action"`
	Findings          []Finding         `json:"findings"`
	Replacements      map[string]string `json:"-"`
	ShortCircuited    bool              `json:"short_circuited"`
	DetectionDegraded bool              `json:"detection_degraded"`
	DurationMS        int64             `json:"duration_ms"`
}

type ModelResult struct {
	Safety     string
	Categories []string
}

type ModelDetector interface {
	Detect(context.Context, string) (ModelResult, error)
}

type Engine struct {
	modelDetector ModelDetector
}

func NewEngine(modelDetector ModelDetector) *Engine {
	return &Engine{modelDetector: modelDetector}
}

func (e *Engine) Evaluate(ctx context.Context, request EvaluationRequest) (decision Decision, err error) {
	started := time.Now()
	decision = Decision{Action: ActionAllow, Replacements: map[string]string{}}
	defer func() { decision.DurationMS = time.Since(started).Milliseconds() }()

	if request.Checkpoint == "" {
		request.Checkpoint = CheckpointBeforeProvider
	}
	if request.Protocol == "" {
		request.Protocol = ProtocolAll
	}
	policies := applicablePolicies(request)
	if len(policies) == 0 {
		return decision, nil
	}

	modelItems := make([]policyItem, 0)
	deterministicPlans := make([]deterministicPlan, 0)
	var deterministicWork uint64
	for _, policy := range policies {
		for _, item := range policy.DetectionItems {
			if item.DetectorType == DetectorModel {
				modelItems = append(modelItems, policyItem{policy: policy, item: item})
				continue
			}
			matchers := deterministicMatchers(item)
			if err := addDeterministicWork(ctx, &deterministicWork, matchers, request.Fragments); err != nil {
				return decision, err
			}
			deterministicPlans = append(deterministicPlans, deterministicPlan{policy: policy, item: item, matchers: matchers})
		}
	}
	remainingFindings := maxDeterministicFindings
	for _, plan := range deterministicPlans {
		findings, err := deterministicFindings(ctx, plan.policy, plan.item, plan.matchers, request.Fragments, &remainingFindings)
		if err != nil {
			return decision, err
		}
		decision.Findings = append(decision.Findings, findings...)
	}

	decision.Action = strictestAction(decision.Findings)
	if decision.Action == ActionBlock {
		decision.ShortCircuited = true
		decision.DurationMS = time.Since(started).Milliseconds()
		return decision, nil
	}

	modelFragments := request.Fragments
	if decision.Action == ActionMask {
		maskedTexts := maskedFragmentTexts(request.Fragments, decision.Findings)
		decision.Replacements = mutableMaskedFragments(request.Fragments, maskedTexts)
		modelFragments = fragmentsWithMaskedText(request.Fragments, maskedTexts)
	}

	if len(modelItems) > 0 {
		result, err := e.detectModel(ctx, modelFragments)
		if err != nil {
			decision.DetectionDegraded = true
			for _, candidate := range modelItems {
				action := ActionAudit
				if stringConfig(candidate.item.Config, "on_unavailable") == ActionBlock {
					action = ActionBlock
				}
				decision.Findings = append(decision.Findings, modelFinding(candidate, action, "model_unavailable", "guardrail_model_unavailable"))
			}
		} else {
			for _, candidate := range modelItems {
				if modelResultMatches(result.Safety, stringConfig(candidate.item.Config, "block_on")) {
					// Model-generated category text is untrusted and may echo prompt
					// content. Persist only the closed safety label in findings.
					category := strings.ToLower(strings.TrimSpace(result.Safety))
					decision.Findings = append(decision.Findings, modelFinding(candidate, candidate.item.Action, category, "guardrail_model_match"))
				}
			}
		}
	}

	decision.Action = strictestAction(decision.Findings)
	decision.DurationMS = time.Since(started).Milliseconds()
	return decision, nil
}

type policyItem struct {
	policy Policy
	item   DetectionItem
}

type deterministicPlan struct {
	policy   Policy
	item     DetectionItem
	matchers []deterministicMatcher
}

func (e *Engine) detectModel(ctx context.Context, fragments []Fragment) (ModelResult, error) {
	if e == nil || e.modelDetector == nil {
		return ModelResult{}, ErrModelUnavailable
	}
	parts := make([]string, 0, len(fragments))
	for _, fragment := range fragments {
		if strings.TrimSpace(fragment.Text) != "" {
			parts = append(parts, fragment.Text)
		}
	}
	return e.modelDetector.Detect(ctx, strings.Join(parts, "\n"))
}

func applicablePolicies(request EvaluationRequest) []Policy {
	result := make([]Policy, 0, len(request.Policies))
	for _, policy := range request.Policies {
		if policy.Status != StatusActive {
			continue
		}
		if request.IgnoreBindings || policyApplies(policy, request.ProjectID, request.Checkpoint, request.Protocol) {
			result = append(result, policy)
		}
	}
	return result
}

func policyApplies(policy Policy, projectID string, checkpoint string, protocol string) bool {
	for _, binding := range policy.Bindings {
		if binding.Checkpoint != checkpoint || binding.Protocol != protocol {
			continue
		}
		if binding.ScopeType == ScopeAllProjects || (binding.ScopeType == ScopeProject && binding.ScopeID == projectID) {
			return true
		}
	}
	return false
}

type deterministicMatcher struct {
	expression   *regexp.Regexp
	captureGroup int
	category     string
	reasonCode   string
	validator    func(string) bool
	workFactor   uint64
}

func deterministicFindings(ctx context.Context, policy Policy, item DetectionItem, matchers []deterministicMatcher, fragments []Fragment, remainingFindings *int) ([]Finding, error) {
	if item.Action != ActionMask {
		for _, fragment := range fragments {
			for _, matcher := range matchers {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if indexes := matcher.firstValidMatch(fragment.Text); indexes != nil {
					if *remainingFindings == 0 {
						return nil, ErrDeterministicWorkBudgetExceeded
					}
					*remainingFindings = *remainingFindings - 1
					return []Finding{newDeterministicFinding(policy, item, fragment.ID, matcher, indexes)}, nil
				}
			}
		}
		return nil, nil
	}

	findings := make([]Finding, 0)
	for _, fragment := range fragments {
		for _, matcher := range matchers {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			matches, exceeded := matcher.allValidMatches(fragment.Text, *remainingFindings)
			if exceeded {
				return nil, ErrDeterministicWorkBudgetExceeded
			}
			*remainingFindings -= len(matches)
			for _, indexes := range matches {
				findings = append(findings, newDeterministicFinding(policy, item, fragment.ID, matcher, indexes))
			}
		}
	}
	return findings, nil
}

func addDeterministicWork(ctx context.Context, totalWork *uint64, matchers []deterministicMatcher, fragments []Fragment) error {
	for _, fragment := range fragments {
		for _, matcher := range matchers {
			if err := ctx.Err(); err != nil {
				return err
			}
			factor := matcher.workFactor
			if factor == 0 {
				factor = 1
			}
			textBytes := uint64(len(fragment.Text))
			if textBytes > maxDeterministicMatcherWork/factor {
				return ErrDeterministicWorkBudgetExceeded
			}
			work := textBytes * factor
			if *totalWork > maxDeterministicEvaluationWork-work {
				return ErrDeterministicWorkBudgetExceeded
			}
			*totalWork += work
		}
	}
	return nil
}

func deterministicMatchers(item DetectionItem) []deterministicMatcher {
	switch item.DetectorType {
	case DetectorPattern:
		return patternMatchers(item)
	case DetectorSensitiveData:
		return sensitiveDataMatchers(item)
	default:
		return nil
	}
}

func patternMatchers(item DetectionItem) []deterministicMatcher {
	matchers := make([]deterministicMatcher, 0)
	caseSensitive := boolConfig(item.Config, "case_sensitive")
	for _, keyword := range stringSliceConfig(item.Config, "keywords") {
		expression := regexp.QuoteMeta(keyword)
		if !caseSensitive {
			expression = "(?i)" + expression
		}
		matchers = appendConfiguredMatcherWithWorkFactor(matchers, expression, 0, "pattern", "guardrail_pattern_match", nil, regexpWorkFactor(expression))
	}
	for _, expression := range stringSliceConfig(item.Config, "regex") {
		matchers = appendConfiguredMatcherWithWorkFactor(matchers, expression, 0, "pattern", "guardrail_pattern_match", nil, regexpWorkFactor(expression))
	}
	return matchers
}

func sensitiveDataMatchers(item DetectionItem) []deterministicMatcher {
	matchers := make([]deterministicMatcher, 0)
	for _, dataType := range stringSliceConfig(item.Config, "data_types") {
		for _, definition := range sensitiveDataDefinitions(dataType) {
			matchers = appendConfiguredMatcher(matchers, definition.expression, definition.captureGroup, dataType, "guardrail_sensitive_data_match", definition.validator)
		}
	}
	return matchers
}

type sensitiveDataDefinition struct {
	expression   string
	captureGroup int
	validator    func(string) bool
}

func sensitiveDataDefinitions(dataType string) []sensitiveDataDefinition {
	switch dataType {
	case "credential":
		return sensitiveDefinitions(
			`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`,
			`(?i)\b(?:sk-[a-z0-9_-]{16,}|gh[pousr]_[a-z0-9]{20,}|glpat-[a-z0-9_-]{20,}|xox[baprs]-[a-z0-9-]{20,})\b`,
			`\bAIza[0-9A-Za-z_-]{35}\b`,
			`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`,
			`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`,
		)
	case "email":
		return sensitiveDefinitions(`(?i)\b[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+\b`)
	case "phone":
		return []sensitiveDataDefinition{
			{expression: `\b1[3-9][0-9]{9}\b`},
			{expression: `(?:^|[^0-9])((?:\+86|86)[- ]?1[3-9][0-9]{9})(?:$|[^0-9])`, captureGroup: 1},
		}
	case "cn_id_card":
		return []sensitiveDataDefinition{{expression: `\b[1-9][0-9]{5}(?:18|19|20)[0-9]{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12][0-9]|3[01])[0-9]{3}[0-9Xx]\b`, validator: validCNIDCard}}
	case "bank_card":
		return []sensitiveDataDefinition{
			{expression: `(?:银行卡号|银行卡|卡号|bank[ _-]*card)\s*[:：]?\s*([0-9](?:[ -]?[0-9]){12,18})`, captureGroup: 1, validator: validCardLength},
			{expression: `\b[0-9](?:[ -]?[0-9]){12,18}\b`, validator: validLuhnCard},
		}
	case "person_name":
		return []sensitiveDataDefinition{{expression: `(?:姓名|联系人|收件人|紧急联系人)\s*[:：]\s*([\p{Han}][\p{Han}·]{1,19})`, captureGroup: 1}}
	case "address":
		return []sensitiveDataDefinition{{expression: `(?:联系地址|家庭住址|户籍地址|收件地址|通讯地址|地址)\s*[:：]\s*([^\r\n]{6,120})`, captureGroup: 1}}
	case "birth_date":
		return []sensitiveDataDefinition{{expression: `(?:出生日期|出生年月|生日)\s*[:：]\s*((?:18|19|20)[0-9]{2}\s*(?:年|[-/.])\s*(?:1[0-2]|0?[1-9])\s*(?:月|[-/.])\s*(?:3[01]|[12][0-9]|0?[1-9])\s*日?)`, captureGroup: 1, validator: validCalendarDate}}
	default:
		return nil
	}
}

func sensitiveDefinitions(expressions ...string) []sensitiveDataDefinition {
	definitions := make([]sensitiveDataDefinition, 0, len(expressions))
	for _, expression := range expressions {
		definitions = append(definitions, sensitiveDataDefinition{expression: expression})
	}
	return definitions
}

func appendConfiguredMatcher(matchers []deterministicMatcher, expression string, captureGroup int, category string, reasonCode string, validator func(string) bool) []deterministicMatcher {
	return appendConfiguredMatcherWithWorkFactor(matchers, expression, captureGroup, category, reasonCode, validator, 1)
}

func appendConfiguredMatcherWithWorkFactor(matchers []deterministicMatcher, expression string, captureGroup int, category string, reasonCode string, validator func(string) bool, workFactor uint64) []deterministicMatcher {
	compiled, err := regexp.Compile(expression)
	if err != nil {
		return matchers
	}
	return append(matchers, deterministicMatcher{
		expression: compiled, captureGroup: captureGroup, category: category,
		reasonCode: reasonCode, validator: validator, workFactor: workFactor,
	})
}

func regexpWorkFactor(expression string) uint64 {
	parsed, err := syntax.Parse(expression, syntax.Perl)
	if err != nil {
		return 1
	}
	program, err := syntax.Compile(parsed.Simplify())
	if err != nil {
		return 1
	}
	// Small RE2 programs count as one full-text scan. Larger keyword and custom
	// regex programs are weighted by compiled instruction count so long literals
	// and counted repetitions cannot hide expensive scans behind short configs.
	factor := (uint64(len(program.Inst)) + regexpInstructionsPerWorkFactor - 1) / regexpInstructionsPerWorkFactor
	if factor == 0 {
		return 1
	}
	return factor
}

func (matcher deterministicMatcher) firstValidMatch(text string) []int {
	for offset := 0; offset <= len(text); {
		indexes := matcher.expression.FindStringSubmatchIndex(text[offset:])
		if indexes == nil {
			return nil
		}
		position := matcher.captureGroup * 2
		if position+1 < len(indexes) && indexes[position] >= 0 && indexes[position+1] > indexes[position] {
			start, end := offset+indexes[position], offset+indexes[position+1]
			if matcher.validator == nil || matcher.validator(text[start:end]) {
				return []int{start, end}
			}
		}
		advance := indexes[1]
		if advance <= indexes[0] {
			advance = indexes[0] + 1
		}
		offset += advance
	}
	return nil
}

func (matcher deterministicMatcher) allValidMatches(text string, limit int) ([][]int, bool) {
	result := make([][]int, 0)
	for offset := 0; offset <= len(text); {
		indexes := matcher.expression.FindStringSubmatchIndex(text[offset:])
		if indexes == nil {
			return result, false
		}
		position := matcher.captureGroup * 2
		if position+1 < len(indexes) && indexes[position] >= 0 && indexes[position+1] > indexes[position] {
			start, end := offset+indexes[position], offset+indexes[position+1]
			if matcher.validator == nil || matcher.validator(text[start:end]) {
				if len(result) == limit {
					return nil, true
				}
				result = append(result, []int{start, end})
			}
		}
		advance := indexes[1]
		if advance <= indexes[0] {
			advance = indexes[0] + 1
		}
		offset += advance
	}
	return result, false
}

func digitsOnly(value string) string {
	var builder strings.Builder
	for _, character := range value {
		if character >= '0' && character <= '9' {
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

func validCardLength(value string) bool {
	length := len(digitsOnly(value))
	return length >= 13 && length <= 19
}

func validCalendarDate(value string) bool {
	digits := digitsOnly(value)
	if len(digits) != 8 {
		return false
	}
	_, err := time.Parse("20060102", digits)
	return err == nil
}

func validLuhnCard(value string) bool {
	digits := digitsOnly(value)
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	parity := len(digits) % 2
	for index, character := range digits {
		digit := int(character - '0')
		if index%2 == parity {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
	}
	return sum%10 == 0
}

func validCNIDCard(value string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 18 {
		return false
	}
	if _, err := time.Parse("20060102", value[6:14]); err != nil {
		return false
	}
	weights := [...]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	checks := "10X98765432"
	sum := 0
	for index := 0; index < 17; index++ {
		digit, err := strconv.Atoi(value[index : index+1])
		if err != nil {
			return false
		}
		sum += digit * weights[index]
	}
	return value[17] == checks[sum%11]
}

func newDeterministicFinding(policy Policy, item DetectionItem, fragmentID string, matcher deterministicMatcher, indexes []int) Finding {
	return Finding{
		PolicyID: policy.ID, PolicyName: policy.Name, ItemID: item.ID, ItemName: item.Name,
		DetectorType: item.DetectorType, Action: item.Action, Category: matcher.category, ReasonCode: matcher.reasonCode,
		FragmentID: fragmentID, Start: indexes[0], End: indexes[1],
	}
}

func modelFinding(candidate policyItem, action string, category string, reasonCode string) Finding {
	return Finding{
		PolicyID: candidate.policy.ID, PolicyName: candidate.policy.Name,
		ItemID: candidate.item.ID, ItemName: candidate.item.Name,
		DetectorType: DetectorModel, Action: action, Category: category, ReasonCode: reasonCode,
	}
}

func modelResultMatches(safety string, blockOn string) bool {
	safety = strings.ToLower(strings.TrimSpace(safety))
	if safety == "unsafe" {
		return true
	}
	return safety == "controversial" && blockOn == "controversial_or_unsafe"
}

func strictestAction(findings []Finding) string {
	result := ActionAllow
	for _, finding := range findings {
		if actionRank(finding.Action) > actionRank(result) {
			result = finding.Action
		}
	}
	return result
}

func actionRank(action string) int {
	switch action {
	case ActionBlock:
		return 3
	case ActionMask:
		return 2
	case ActionAudit:
		return 1
	default:
		return 0
	}
}

func maskedFragments(fragments []Fragment, findings []Finding) map[string]string {
	return mutableMaskedFragments(fragments, maskedFragmentTexts(fragments, findings))
}

func maskedFragmentTexts(fragments []Fragment, findings []Finding) map[string]string {
	byFragment := map[string][]Finding{}
	for _, finding := range findings {
		if finding.Action == ActionMask && finding.End > finding.Start {
			byFragment[finding.FragmentID] = append(byFragment[finding.FragmentID], finding)
		}
	}
	result := map[string]string{}
	for _, fragment := range fragments {
		if len(byFragment[fragment.ID]) == 0 {
			continue
		}
		spans := byFragment[fragment.ID]
		sort.Slice(spans, func(i int, j int) bool {
			if spans[i].Start != spans[j].Start {
				return spans[i].Start < spans[j].Start
			}
			return spans[i].End > spans[j].End
		})
		merged := make([]Finding, 0, len(spans))
		for _, span := range spans {
			if span.Start < 0 || span.End > len(fragment.Text) || span.End <= span.Start {
				continue
			}
			if len(merged) == 0 || span.Start > merged[len(merged)-1].End {
				merged = append(merged, span)
				continue
			}
			if span.End > merged[len(merged)-1].End {
				merged[len(merged)-1].End = span.End
			}
		}
		var builder strings.Builder
		cursor := 0
		for _, span := range merged {
			builder.WriteString(fragment.Text[cursor:span.Start])
			builder.WriteString("[REDACTED]")
			cursor = span.End
		}
		builder.WriteString(fragment.Text[cursor:])
		result[fragment.ID] = builder.String()
	}
	return result
}

func mutableMaskedFragments(fragments []Fragment, maskedTexts map[string]string) map[string]string {
	result := map[string]string{}
	for _, fragment := range fragments {
		if !fragment.Mutable {
			continue
		}
		if maskedText, ok := maskedTexts[fragment.ID]; ok {
			result[fragment.ID] = maskedText
		}
	}
	return result
}

func fragmentsWithMaskedText(fragments []Fragment, maskedTexts map[string]string) []Fragment {
	if len(maskedTexts) == 0 {
		return fragments
	}
	result := append([]Fragment(nil), fragments...)
	for index := range result {
		if maskedText, ok := maskedTexts[result[index].ID]; ok {
			result[index].Text = maskedText
		}
	}
	return result
}

func stringSliceConfig(config map[string]any, key string) []string {
	values, _ := config[key].([]any)
	if typed, ok := config[key].([]string); ok {
		return typed
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func stringConfig(config map[string]any, key string) string {
	value, _ := config[key].(string)
	return strings.ToLower(strings.TrimSpace(value))
}

func boolConfig(config map[string]any, key string) bool {
	value, _ := config[key].(bool)
	return value
}
