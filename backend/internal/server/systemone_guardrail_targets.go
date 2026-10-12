package server

import (
	"encoding/json"
	"fmt"
	"sort"
)

type systemOneGuardrailBatch struct {
	targets []guardrailTextTarget
	updates []func() error
	budget  systemOneJSONBudget
}

func systemOneGuardrailTargets(req *SystemOneRequest) (*systemOneGuardrailBatch, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	batch := &systemOneGuardrailBatch{}
	if err := batch.entry(req.State, "state", systemOneMaxJSONDepth, func(next json.RawMessage) { req.State = next }, &req.unsafeRedaction); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(req.Questions))
	for id := range req.Questions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for index, id := range ids {
		question := req.Questions[id]
		prefix := fmt.Sprintf("questions.%d", index)
		if err := batch.target(id, prefix+".id", func(any) { req.unsafeRedaction = true }); err != nil {
			return nil, err
		}
		if err := batch.entry(question.Instructions, prefix+".instructions", systemOneMaxJSONDepth, func(next json.RawMessage) {
			q := req.Questions[id]
			q.Instructions = next
			req.Questions[id] = q
		}, &req.unsafeRedaction); err != nil {
			return nil, err
		}
		if err := batch.entry(question.Criteria, prefix+".criteria", systemOneMaxJSONDepth+1, func(next json.RawMessage) {
			q := req.Questions[id]
			q.Criteria = next
			req.Questions[id] = q
		}, &req.unsafeRedaction); err != nil {
			return nil, err
		}
	}
	return batch, nil
}

func (b *systemOneGuardrailBatch) target(text, id string, set func(any)) error {
	if len(b.targets) >= systemOneMaxJSONNodes {
		return systemOneInvalid("request exceeds the System One guardrail target limit")
	}
	appendGuardrailStringTarget(&b.targets, text, id, set)
	return nil
}

func (b *systemOneGuardrailBatch) entry(raw json.RawMessage, id string, maxDepth int, set func(json.RawMessage), unsafe *bool) error {
	if len(raw) == 0 {
		return nil
	}
	value, err := b.budget.decode(raw, maxDepth)
	if err != nil {
		return systemOneInvalid("request JSON exceeds resource limits or contains duplicate members")
	}
	dirty := false
	if err := b.walk(value, id, func(next any) { value = next }, &dirty, unsafe); err != nil {
		return err
	}
	// Replacements update the parsed tree. Serialize each changed entry once,
	// after all targets have been evaluated, rather than rebuilding every ancestor.
	b.updates = append(b.updates, func() error {
		if !dirty {
			return nil
		}
		encoded, err := json.Marshal(value)
		if err == nil {
			set(encoded)
		}
		return err
	})
	return nil
}

func (b *systemOneGuardrailBatch) walk(value any, id string, set func(any), dirty, unsafe *bool) error {
	switch typed := value.(type) {
	case string:
		return b.target(typed, id, func(next any) { set(next); *dirty = true })
	case json.Number:
		blockMask := func(any) { *unsafe = true }
		if err := b.target(typed.String(), id, blockMask); err != nil {
			return err
		}
		if expanded := systemOneGuardrailIntegerText(typed); expanded != typed.String() {
			return b.target(expanded, id+".integer", blockMask)
		}
	case []any:
		for index, child := range typed {
			if err := b.walk(child, fmt.Sprintf("%s.%d", id, index), func(next any) { typed[index] = next }, dirty, unsafe); err != nil {
				return err
			}
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for index, key := range keys {
			path := fmt.Sprintf("%s.%d", id, index)
			if err := b.target(key, path+".key", func(any) { *unsafe = true }); err != nil {
				return err
			}
			if err := b.walk(typed[key], path+".value", func(next any) { typed[key] = next }, dirty, unsafe); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *systemOneGuardrailBatch) apply() error {
	for _, update := range b.updates {
		if err := update(); err != nil {
			return err
		}
	}
	return nil
}
