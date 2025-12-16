package trigger

import (
	"context"
	"fmt"
	"sync"
	"syntrix/internal/storage"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
)

// Evaluator is responsible for matching events against trigger conditions.
type Evaluator interface {
	Evaluate(ctx context.Context, trigger *Trigger, event *storage.Event) (bool, error)
}

// CELEvaluator implements Evaluator using Google CEL.
type CELEvaluator struct {
	env        *cel.Env
	prgCache   map[string]cel.Program
	cacheMutex sync.RWMutex
}

func NewCELEvaluator() (*CELEvaluator, error) {
	// Define the CEL environment with an 'event' variable
	env, err := cel.NewEnv(
		cel.Declarations(
			decls.NewVar("event", decls.NewMapType(decls.String, decls.Dyn)),
		),
	)
	if err != nil {
		return nil, err
	}

	return &CELEvaluator{
		env:      env,
		prgCache: make(map[string]cel.Program),
	}, nil
}

func (e *CELEvaluator) Evaluate(ctx context.Context, trigger *Trigger, event *storage.Event) (bool, error) {
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
	if event.Document != nil && trigger.Collection != event.Document.Collection {
		return false, nil
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
			"path":      event.Path,
			"timestamp": event.Timestamp,
			"document":  event.Document, // This relies on Document struct fields being accessible or converted
		},
	}

	// If Document is struct, we might need to convert it to map or rely on CEL's reflection if configured.
	// For simplicity, let's manually construct the map for the document part we care about.
	if event.Document != nil {
		docMap := map[string]interface{}{
			"id":         event.Document.Id,
			"collection": event.Document.Collection,
			"data":       event.Document.Data,
			"version":    event.Document.Version,
		}
		input["event"].(map[string]interface{})["document"] = docMap
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

func (e *CELEvaluator) getProgram(condition string) (cel.Program, error) {
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
