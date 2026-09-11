package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/puller/buffer"
	"github.com/syntrixbase/syntrix/internal/puller/config"
	"github.com/syntrixbase/syntrix/internal/puller/cursor"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type idleBoundaryStream struct {
	token  bson.Raw
	ctx    context.Context
	polled bool
}

func (s *idleBoundaryStream) TryNext(ctx context.Context) bool {
	s.ctx = ctx
	if !s.polled {
		s.polled = true
		return false
	}
	<-ctx.Done()
	return false
}
func (s *idleBoundaryStream) ID() int64                   { return 1 }
func (s *idleBoundaryStream) ResumeToken() bson.Raw       { return s.token }
func (s *idleBoundaryStream) Decode(any) error            { return nil }
func (s *idleBoundaryStream) Err() error                  { return s.ctx.Err() }
func (s *idleBoundaryStream) Close(context.Context) error { return nil }

func TestNativeEmptyBoundarySurvivesRestartAndReplaysBeforeReady(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dir := t.TempDir()
	b, err := buffer.New(buffer.Options{Path: dir, BatchInterval: time.Hour})
	require.NoError(t, err)
	p := New(config.DefaultConfig(), nil)
	backend := &Backend{name: "source", buffer: b}
	p.backends["source"] = backend
	token := bson.Raw{5, 0, 0, 0, 0}
	p.openStream = func(context.Context, *mongo.Database, mongo.Pipeline, *options.ChangeStreamOptions) (changeStream, error) {
		return &idleBoundaryStream{token: token}, nil
	}
	captureCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- p.watchChangeStream(captureCtx, backend, p.logger) }()
	boundary, err := p.BootstrapBoundary(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, boundary)
	pm, err := cursor.DecodeProgressMarker(boundary)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"source": ""}, pm.Positions)
	require.Equal(t, b.Lineage(), pm.Lineages["source"])
	stop()
	require.ErrorIs(t, <-done, context.Canceled)
	require.NoError(t, b.Close())
	b, err = buffer.New(buffer.Options{Path: dir})
	require.NoError(t, err)
	defer b.Close()
	restored, err := b.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, token, restored)
	backend.buffer = b
	backend.setCaptureState(true, nil)
	evt := &events.StoreChangeEvent{Backend: "source", EventID: "1-1-a", ClusterTime: events.ClusterTime{T: 1, I: 1}}
	require.NoError(t, b.Write(ctx, evt, token))
	require.NoError(t, b.Flush(ctx))
	ready := make(chan string, 1)
	subscription := p.SubscribeReady(ctx, "indexer", boundary, func(progress string) { ready <- progress })
	select {
	case received := <-subscription:
		require.Equal(t, evt.EventID, received.Change.EventID)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case progress := <-ready:
		require.NoError(t, p.ValidateBoundary(ctx, progress))
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, b.Delete(evt.BufferKey()))
	require.ErrorContains(t, p.ValidateBoundary(ctx, boundary), "expired")
	wrong := pm.Clone()
	wrong.Lineages["source"] = "changed"
	require.ErrorContains(t, p.ValidateBoundary(ctx, wrong.Encode()), "lineage")
	_, err = p.BootstrapBoundary(ctx)
	require.Error(t, err)
}

type gatedBoundaryStream struct {
	idleBoundaryStream
	entered chan struct{}
	release chan struct{}
}

func (s *gatedBoundaryStream) TryNext(ctx context.Context) bool {
	if !s.polled {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			s.ctx = ctx
			return false
		}
	}
	return s.idleBoundaryStream.TryNext(ctx)
}

func TestNativeBoundaryDoesNotTrustCachedResumeTokenBeforeLiveBatch(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	b, err := buffer.New(buffer.Options{Path: t.TempDir()})
	require.NoError(t, err)
	defer b.Close()
	p := New(config.DefaultConfig(), nil)
	backend := &Backend{name: "source", buffer: b}
	p.backends["source"] = backend
	stream := &gatedBoundaryStream{idleBoundaryStream: idleBoundaryStream{token: bson.Raw{5, 0, 0, 0, 0}}, entered: make(chan struct{}), release: make(chan struct{})}
	p.openStream = func(context.Context, *mongo.Database, mongo.Pipeline, *options.ChangeStreamOptions) (changeStream, error) {
		return stream, nil
	}
	done := make(chan error, 1)
	go func() { done <- p.watchChangeStream(ctx, backend, p.logger) }()
	select {
	case <-stream.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	token, err := b.LoadCheckpoint()
	require.NoError(t, err)
	require.Empty(t, token)
	backend.captureMu.Lock()
	active := backend.captureActive
	backend.captureMu.Unlock()
	require.False(t, active)
	close(stream.release)
	boundary, err := p.BootstrapBoundary(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, boundary)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}
