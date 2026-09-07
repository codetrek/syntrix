package cursor

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/syntrixbase/syntrix/internal/puller/events"
)

const FormatVersion = 1
const maxEncodedMarkerBytes = 64 * 1024

// Position is comparable only within one source and continuity generation.
// Sequence zero denotes the beginning of that generation.
type Position struct {
	SourceID   string `json:"source"`
	Generation string `json:"generation"`
	Sequence   uint64 `json:"sequence"`
}

func (p Position) Validate() error {
	if p.SourceID == "" || p.Generation == "" {
		return invalid(errors.New("source and generation are required"))
	}
	return nil
}

// ProgressMarker describes a delivered prefix for each source. Consumers persist
// the opaque marker after applying that prefix; arrival alone is not processing.
type ProgressMarker struct {
	Version   int                 `json:"v"`
	Positions map[string]Position `json:"positions"`
}

func NewProgressMarker() *ProgressMarker {
	return &ProgressMarker{Version: FormatVersion, Positions: make(map[string]Position)}
}

func invalid(cause error) error {
	return &events.Error{Code: events.CodeInvalidCursor, Cause: cause}
}

func DecodeProgressMarker(encoded string) (*ProgressMarker, error) {
	if encoded == "" {
		return NewProgressMarker(), nil
	}
	if len(encoded) > maxEncodedMarkerBytes {
		return nil, invalid(errors.New("cursor exceeds encoded size limit"))
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, invalid(fmt.Errorf("decode cursor: %w", err))
	}
	type wirePosition struct {
		SourceID   string  `json:"source"`
		Generation string  `json:"generation"`
		Sequence   *uint64 `json:"sequence"`
	}
	var wire struct {
		Version   int                     `json:"v"`
		Positions map[string]wirePosition `json:"positions"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return nil, invalid(fmt.Errorf("decode cursor fields: %w", err))
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, invalid(errors.New("cursor contains trailing data"))
	}
	if wire.Version != FormatVersion {
		return nil, &events.Error{Code: events.CodeUnsupportedFormat, Cause: fmt.Errorf("cursor format %d is unsupported", wire.Version)}
	}
	if len(wire.Positions) == 0 {
		return nil, invalid(errors.New("saved cursor requires source positions"))
	}
	result := NewProgressMarker()
	for key, value := range wire.Positions {
		if value.Sequence == nil {
			return nil, invalid(errors.New("cursor sequence is required"))
		}
		position := Position{SourceID: value.SourceID, Generation: value.Generation, Sequence: *value.Sequence}
		if err := position.Validate(); err != nil {
			return nil, err
		}
		if key != position.SourceID {
			return nil, invalid(errors.New("cursor source key does not match position"))
		}
		result.Positions[key] = position
	}
	return result, nil
}

func (p *ProgressMarker) Encode() (string, error) {
	if p == nil || p.Version != FormatVersion {
		return "", &events.Error{Code: events.CodeUnsupportedFormat, Cause: errors.New("unsupported cursor format")}
	}
	if len(p.Positions) == 0 {
		return "", invalid(errors.New("saved cursor requires source positions"))
	}
	for key, position := range p.Positions {
		if err := position.Validate(); err != nil {
			return "", err
		}
		if key != position.SourceID {
			return "", invalid(errors.New("cursor source key does not match position"))
		}
	}
	data, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	if len(encoded) > maxEncodedMarkerBytes {
		return "", invalid(errors.New("cursor exceeds encoded size limit"))
	}
	return encoded, nil
}

func (p *ProgressMarker) SetPosition(position Position) {
	p.Positions[position.SourceID] = position
}

func (p *ProgressMarker) GetPosition(sourceID string) (Position, bool) {
	position, exists := p.Positions[sourceID]
	return position, exists
}

func (p *ProgressMarker) Clone() *ProgressMarker {
	result := NewProgressMarker()
	result.Version = p.Version
	for key, position := range p.Positions {
		result.Positions[key] = position
	}
	return result
}
