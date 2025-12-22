package trigger

import (
	"context"
	"fmt"
	"path"
	"sync"
	"syntrix/internal/storage"

	"github.com/google/cel-go/cel"
)

// Evaluator is responsible for matching events against trigger conditions.
type Evaluator interface {
	Evaluate(ctx context.Context, trigger *Trigger, event *storage.Event) (bool, error)
}

// celeEvaluator implements Evaluator using Google CEL.
type celeEvaluator struct {
	env        *cel.Env
	prgCache   map[string]cel.Program
	cacheMutex sync.RWMutex
}

func NewEvaluator() (Evaluator, error) {
	// Define the CEL environment with an 'event' variable
	env, err := cel.NewEnv(
		cel.Variable("event", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		return nil, err
	}

	return &celeEvaluator{
		env:      env,
		prgCache: make(map[string]cel.Program),
	}, nil
}

func (e *celeEvaluator) Evaluate(ctx context.Context, trigger *Trigger, event *storage.Event) (bool, error) {
	// 1. Check event type (create, update, delete)
	eventTypeMatch := false
	for _, evt := range trigger.Events {
		if string(event.Type) == evt {
			eventTypeMatch = true
			break
		}
	}
	if !eventTypeMatch {
		return false, nil
	}

	// 2. Check collection
	// Handle case where event.Document might be nil (e.g. delete event might only have ID/Path)
	// But for now assuming Document is present or we check Path/Collection from somewhere else.
	// The storage.Event struct has Document *Document.
	if event.Document != nil {
		matched, err := path.Match(trigger.Collection, event.Document.Collection)
		if err != nil {
			return false, fmt.Errorf("invalid collection pattern: %w", err)
		}
		if !matched {
			return false, nil
		}
	}

	// 3. Evaluate CEL condition
	if trigger.Condition == "" {
		return true, nil
	}

	prg, err := e.getProgram(trigger.Condition)
	if err != nil {
		return false, fmt.Errorf("failed to get CEL program: %w", err)
	}

	// Prepare input
	input := map[string]interface{}{
		"event": map[string]interface{}{
			"type":      string(event.Type),
			"timestamp": event.Timestamp,
			"document":  nil,
			"before":    nil,
		},
	}

	// If Document is struct, we might need to convert it to map or rely on CEL's reflection if configured.
	// For simplicity, let's manually construct the map for the document part we care about.
	if event.Document != nil {
		docMap := make(map[string]interface{})
		// Flatten data fields
		for k, v := range event.Document.Data {
			docMap[k] = v
		}
		// Set system fields (these overwrite data fields if collision occurs, which is expected behavior for reserved fields)
		docMap["id"] = event.Document.Id
		docMap["collection"] = event.Document.Collection
		docMap["version"] = event.Document.Version

		input["event"].(map[string]interface{})["document"] = docMap
	}

	if event.Before != nil {
		docMap := make(map[string]interface{})
		// Flatten data fields
		for k, v := range event.Before.Data {
			docMap[k] = v
		}
		// Set system fields
		docMap["id"] = event.Before.Id
		docMap["collection"] = event.Before.Collection
		docMap["version"] = event.Before.Version

		input["event"].(map[string]interface{})["before"] = docMap
	}

	out, _, err := prg.Eval(input)
	if err != nil {
		return false, fmt.Errorf("CEL evaluation error: %w", err)
	}

	match, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("CEL condition must return boolean, got %T", out.Value())
	}

	return match, nil
}

func (e *celeEvaluator) getProgram(condition string) (cel.Program, error) {
	e.cacheMutex.RLock()
	prg, ok := e.prgCache[condition]
	e.cacheMutex.RUnlock()
	if ok {
		return prg, nil
	}

	e.cacheMutex.Lock()
	defer e.cacheMutex.Unlock()

	// Double check
	if prg, ok := e.prgCache[condition]; ok {
		return prg, nil
	}

	ast, issues := e.env.Compile(condition)
	if issues != nil && issues.Err() != nil {
		return nil, issues.Err()
	}

	prg, err := e.env.Program(ast)
	if err != nil {
		return nil, err
	}

	e.prgCache[condition] = prg
	return prg, nil
}
