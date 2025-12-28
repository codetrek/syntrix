package types

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFatalError(t *testing.T) {
	baseErr := errors.New("base error")
	fatalErr := &FatalError{Err: baseErr}

	assert.Equal(t, "fatal error: base error", fatalErr.Error())
	assert.Equal(t, baseErr, fatalErr.Unwrap())
}

func TestIsFatal(t *testing.T) {
	baseErr := errors.New("base error")
	fatalErr := &FatalError{Err: baseErr}
	wrappedErr := fmt.Errorf("wrapped: %w", fatalErr)

	assert.True(t, IsFatal(fatalErr))
	assert.True(t, IsFatal(wrappedErr))
	assert.False(t, IsFatal(baseErr))
	assert.False(t, IsFatal(nil))
}
