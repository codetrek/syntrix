package checkpoint

import (
	"bytes"
	"encoding/binary"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/bson/bsontype"
)

const maxTokenDepth = 100

// The driver's Raw.Validate checks value lengths without recursively validating
// their contents. We validate the BSON grammar without decoding or rewriting
// native token fields: https://bsonspec.org/spec.html.
func validateBSONDocument(data []byte, array bool, depth int) error {
	if depth > maxTokenDepth || len(data) < 5 || binary.LittleEndian.Uint32(data[:4]) != uint32(len(data)) || data[len(data)-1] != 0 {
		return invalidBSON()
	}
	seen := make(map[string]struct{})
	remaining := data[4 : len(data)-1]
	for len(remaining) > 0 {
		kind := bsontype.Type(remaining[0])
		remaining = remaining[1:]
		keySize, err := bsonCStringSize(remaining)
		if err != nil {
			return err
		}
		key := string(remaining[:keySize-1])
		if _, duplicate := seen[key]; duplicate || (array && key != strconv.Itoa(len(seen))) {
			return invalidBSON()
		}
		seen[key] = struct{}{}
		remaining = remaining[keySize:]
		valueSize, err := bsonValueSize(kind, remaining, depth)
		if err != nil {
			return err
		}
		remaining = remaining[valueSize:]
	}
	return nil
}

func bsonValueSize(kind bsontype.Type, data []byte, depth int) (int, error) {
	var size int
	switch kind {
	case bsontype.EmbeddedDocument, bsontype.Array:
		size, err := bsonLengthSize(data, 5, 0)
		if err != nil {
			return 0, err
		}
		return size, validateBSONDocument(data[:size], kind == bsontype.Array, depth+1)
	case bsontype.CodeWithScope:
		size, err := bsonLengthSize(data, 14, 0)
		if err != nil {
			return 0, err
		}
		codeSize, err := bsonStringSize(data[4:size])
		if err != nil {
			return 0, err
		}
		return size, validateBSONDocument(data[4+codeSize:size], false, depth+1)
	case bsontype.String, bsontype.JavaScript, bsontype.Symbol:
		return bsonStringSize(data)
	case bsontype.DBPointer:
		stringSize, err := bsonStringSize(data)
		if err != nil {
			return 0, err
		}
		size = stringSize + 12
	case bsontype.Binary:
		size, err := bsonLengthSize(data, 0, 5)
		if err != nil {
			return 0, err
		}
		if data[4] == 2 && (size < 9 || binary.LittleEndian.Uint32(data[5:9]) != uint32(size-9)) {
			return 0, invalidBSON()
		}
		return size, nil
	case bsontype.Regex:
		patternSize, err := bsonCStringSize(data)
		if err != nil {
			return 0, err
		}
		optionSize, err := bsonCStringSize(data[patternSize:])
		if err != nil {
			return 0, err
		}
		options := data[patternSize : patternSize+optionSize-1]
		for i, option := range options {
			if !strings.ContainsRune("imsux", rune(option)) || (i > 0 && option <= options[i-1]) {
				return 0, invalidBSON()
			}
		}
		return patternSize + optionSize, nil
	case bsontype.Boolean:
		if len(data) == 0 || data[0] > 1 {
			return 0, invalidBSON()
		}
		size = 1
	case bsontype.Int32:
		size = 4
	case bsontype.DateTime, bsontype.Double, bsontype.Int64, bsontype.Timestamp:
		size = 8
	case bsontype.ObjectID:
		size = 12
	case bsontype.Decimal128:
		size = 16
	case bsontype.MaxKey, bsontype.MinKey, bsontype.Null, bsontype.Undefined:
		size = 0
	default:
		return 0, invalidBSON()
	}
	if len(data) < size {
		return 0, invalidBSON()
	}
	return size, nil
}

func bsonLengthSize(data []byte, minimum, extra int) (int, error) {
	if len(data) < 4 {
		return 0, invalidBSON()
	}
	length := uint64(binary.LittleEndian.Uint32(data[:4]))
	if length < uint64(minimum) || length+uint64(extra) > uint64(len(data)) {
		return 0, invalidBSON()
	}
	return int(length) + extra, nil
}

func bsonStringSize(data []byte) (int, error) {
	size, err := bsonLengthSize(data, 1, 4)
	if err != nil {
		return 0, err
	}
	if data[size-1] != 0 || !utf8.Valid(data[4:size-1]) {
		return 0, invalidBSON()
	}
	return size, nil
}

func bsonCStringSize(data []byte) (int, error) {
	size := bytes.IndexByte(data, 0)
	if size < 0 || !utf8.Valid(data[:size]) {
		return 0, invalidBSON()
	}
	return size + 1, nil
}

func invalidBSON() error {
	return &Error{Code: InvalidCheckpoint, Message: "MongoDB resume token BSON structure is invalid"}
}
