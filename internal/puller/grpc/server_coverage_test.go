package grpc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestConvertEventPreservesPayloadAndRejectsUnencodableData(t *testing.T) {
	t.Parallel()
	txn := int64(123)
	source := &events.PullerEvent{Progress: "checkpoint", Change: &events.StoreChangeEvent{
		EventID: "source-event", Database: "database", MgoColl: "documents", MgoDocID: "doc", OpType: events.StoreOperationUpdate,
		FullDocument: &storage.StoredDoc{Id: "doc", Data: map[string]any{"value": "new"}},
		UpdateDesc:   &events.UpdateDescription{UpdatedFields: map[string]any{"value": "new"}},
		ClusterTime:  events.ClusterTime{T: 10, I: 2}, Timestamp: 1000, TxnNumber: &txn, Backend: "backend",
	}}
	got, err := convertEvent(source)
	require.NoError(t, err)
	assert.Equal(t, "checkpoint", got.Progress)
	assert.Equal(t, "source-event", got.ChangeEvent.EventId)
	assert.Equal(t, txn, got.ChangeEvent.TxnNumber)
	assert.JSONEq(t, `{"updatedFields":{"value":"new"}}`, string(got.ChangeEvent.UpdateDesc))
	assert.Contains(t, string(got.ChangeEvent.FullDoc), `"value":"new"`)
	source.Change.FullDocument.Data["invalid"] = make(chan struct{})
	_, err = convertEvent(source)
	require.ErrorContains(t, err, "marshal full document")
	source.Change.FullDocument = nil
	source.Change.UpdateDesc.UpdatedFields["invalid"] = make(chan struct{})
	_, err = convertEvent(source)
	require.ErrorContains(t, err, "marshal update description")
	_, err = convertEvent(nil)
	require.Error(t, err)
}

func TestTransportErrorPreservesContextAndStatus(t *testing.T) {
	t.Parallel()
	assert.Equal(t, codes.Canceled, status.Code(transportError(context.Canceled)))
	assert.Equal(t, codes.DeadlineExceeded, status.Code(transportError(context.DeadlineExceeded)))
	original := status.Error(codes.PermissionDenied, "denied")
	assert.Same(t, original, transportError(original))
	assert.Equal(t, codes.Internal, status.Code(transportError(errors.New("failure"))))
}
