package cursor

import (
	"encoding/base64"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/puller/events"
)

func TestProgressRoundTripAndIsolation(t *testing.T) {
	original := NewProgressMarker()
	original.SetPosition(Position{SourceID: "source-a", Generation: "generation-1", Sequence: math.MaxUint64})
	original.SetPosition(Position{SourceID: "source-b", Generation: "generation-2", Sequence: 0})
	encoded, err := original.Encode()
	require.NoError(t, err)
	decoded, err := DecodeProgressMarker(encoded)
	require.NoError(t, err)
	assert.Equal(t, original, decoded)
	copy := decoded.Clone()
	copy.SetPosition(Position{SourceID: "source-a", Generation: "generation-1", Sequence: 1})
	position, exists := original.GetPosition("source-a")
	require.True(t, exists)
	assert.Equal(t, uint64(math.MaxUint64), position.Sequence)
	_, exists = original.GetPosition("absent")
	assert.False(t, exists)
}

func TestSavedCursorRejectsMalformedOrUnsupportedState(t *testing.T) {
	cases := map[string]struct {
		body string
		code events.ErrorCode
	}{
		"legacy":             {`{"p":{"backend":"event-id"}}`, events.CodeInvalidCursor},
		"version":            {`{"v":2,"positions":{}}`, events.CodeUnsupportedFormat},
		"missing positions":  {`{"v":1}`, events.CodeInvalidCursor},
		"missing sequence":   {`{"v":1,"positions":{"a":{"source":"a","generation":"g"}}}`, events.CodeInvalidCursor},
		"missing source":     {`{"v":1,"positions":{"a":{"generation":"g","sequence":0}}}`, events.CodeInvalidCursor},
		"missing generation": {`{"v":1,"positions":{"a":{"source":"a","sequence":0}}}`, events.CodeInvalidCursor},
		"mismatched key":     {`{"v":1,"positions":{"b":{"source":"a","generation":"g","sequence":0}}}`, events.CodeInvalidCursor},
		"negative":           {`{"v":1,"positions":{"a":{"source":"a","generation":"g","sequence":-1}}}`, events.CodeInvalidCursor},
		"overflow":           {`{"v":1,"positions":{"a":{"source":"a","generation":"g","sequence":18446744073709551616}}}`, events.CodeInvalidCursor},
		"unknown field":      {`{"v":1,"positions":{},"extra":true}`, events.CodeInvalidCursor},
		"trailing":           {`{"v":1,"positions":{}} {}`, events.CodeInvalidCursor},
	}
	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeProgressMarker(base64.RawURLEncoding.EncodeToString([]byte(item.body)))
			var typed *events.Error
			require.ErrorAs(t, err, &typed)
			assert.Equal(t, item.code, typed.Code)
		})
	}
	for _, encoded := range []string{"invalid!!!", strings.Repeat("a", maxEncodedMarkerBytes+1)} {
		_, err := DecodeProgressMarker(encoded)
		require.Error(t, err)
	}
	empty, err := DecodeProgressMarker("")
	require.NoError(t, err)
	assert.Empty(t, empty.Positions)
	_, err = empty.Encode()
	require.Error(t, err)
}

func TestEncodingRejectsInvalidPositions(t *testing.T) {
	var absent *ProgressMarker
	_, err := absent.Encode()
	require.Error(t, err)
	for _, position := range []Position{{Generation: "g"}, {SourceID: "s"}} {
		marker := NewProgressMarker()
		marker.SetPosition(position)
		_, err := marker.Encode()
		require.Error(t, err)
	}
	marker := NewProgressMarker()
	marker.Positions["other"] = Position{SourceID: "source", Generation: "g"}
	_, err = marker.Encode()
	require.Error(t, err)
	marker = NewProgressMarker()
	marker.SetPosition(Position{SourceID: strings.Repeat("s", maxEncodedMarkerBytes), Generation: "g"})
	_, err = marker.Encode()
	require.Error(t, err)
}

func TestDomainErrorPreservesCause(t *testing.T) {
	cause := errors.New("storage synchronization failed")
	err := &events.Error{Code: events.CodeStorageFailure, SourceID: "source", Generation: "g", Backend: "mongo", Cause: cause}
	assert.ErrorIs(t, err, cause)
	assert.Contains(t, err.Error(), "STORAGE_FAILURE")
	assert.Contains(t, err.Error(), "storage synchronization failed")
}
