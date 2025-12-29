package engine

import (
	"testing"

	"github.com/codetrek/syntrix/internal/trigger/types"
	"github.com/stretchr/testify/assert"
)

func TestNewFactory_Coverage(t *testing.T) {
	// Case 1: No options
	f, err := NewFactory(nil, nil, nil)
	assert.NoError(t, err)
	assert.NotNil(t, f)

	// Case 2: With options
	f, err = NewFactory(nil, nil, nil, WithMetrics(&types.NoopMetrics{}))
	assert.NoError(t, err)
	assert.NotNil(t, f)
}
