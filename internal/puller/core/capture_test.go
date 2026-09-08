package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/puller/checkpoint"
	"github.com/syntrixbase/syntrix/internal/puller/config"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"github.com/syntrixbase/syntrix/internal/puller/normalizer"
	"github.com/syntrixbase/syntrix/internal/puller/recovery"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func captureTestToken(t *testing.T, position string) bson.Raw {
	t.Helper()
	raw, err := bson.Marshal(bson.D{{Key: "_data", Value: position}, {Key: "extra", Value: primitive.Binary{Subtype: 0, Data: []byte{1, 2, 3}}}})
	require.NoError(t, err)
	return raw
}

func captureFixture(t *testing.T) (*Puller, *Backend, checkpoint.MongoSource) {
	t.Helper()
	cfg := newTestConfig(t)
	cfg.Buffer.BatchInterval = time.Hour
	cfg.Buffer.BatchSize = 1000
	p := New(cfg, nil)
	client, err := mongo.NewClient(options.Client().ApplyURI(testMongoURI))
	require.NoError(t, err)
	require.NoError(t, p.AddBackend("source", client, "database", config.PullerBackendConfig{Collections: []string{"users"}}))
	t.Cleanup(func() { require.NoError(t, p.backends["source"].buffer.Close()) })
	source := checkpoint.MongoSource{ID: "source", Database: "database", Collections: []checkpoint.MongoCollection{{Name: "users", UUID: "01010101010101010101010101010101"}}}
	p.readSource = func(context.Context, *mongo.Database, string, []string) (checkpoint.MongoSource, error) {
		return source, nil
	}
	return p, p.backends["source"], source
}

type captureStep struct {
	event  *normalizer.RawEvent
	token  bson.Raw
	err    error
	before func()
}

type captureStream struct {
	token    bson.Raw
	steps    []captureStep
	current  captureStep
	closed   bool
	closeErr error
	calls    int
}

func (s *captureStream) TryNext(context.Context) bool {
	s.calls++
	if len(s.steps) == 0 {
		s.current = captureStep{err: context.Canceled}
		return false
	}
	s.current, s.steps = s.steps[0], s.steps[1:]
	if s.current.before != nil {
		s.current.before()
	}
	if s.current.token != nil {
		s.token = s.current.token
	}
	return s.current.event != nil
}
func (s *captureStream) ResumeToken() bson.Raw { return s.token }
func (s *captureStream) ID() int64             { return 1 }
func (s *captureStream) Err() error            { return s.current.err }
func (s *captureStream) Decode(out any) error {
	*out.(*normalizer.RawEvent) = *s.current.event
	return nil
}
func (s *captureStream) Close(ctx context.Context) error { s.closed = true; return s.closeErr }

func attachCaptureStream(p *Puller, stream changeStream) {
	p.openStream = func(context.Context, *mongo.Database, mongo.Pipeline, *options.ChangeStreamOptions) (changeStream, error) {
		return stream, nil
	}
}

func captureEvent(t *testing.T, token bson.Raw) *normalizer.RawEvent {
	t.Helper()
	raw := &normalizer.RawEvent{OperationType: "insert", ClusterTime: primitive.Timestamp{T: 100, I: 1}, DocumentKey: bson.M{"_id": "doc"}, ResumeToken: token}
	raw.Namespace.DB, raw.Namespace.Coll = "database", "users"
	return raw
}

func requireCaptureCode(t *testing.T, err error, code checkpoint.ErrorCode) {
	t.Helper()
	var typed *checkpoint.Error
	require.ErrorAs(t, err, &typed)
	require.Equal(t, code, typed.Code)
	require.Equal(t, recovery.ActionFatal, recovery.NewHandler(recovery.HandlerOptions{}).HandleError(err))
}

func TestCaptureFreshAndIdlePositions(t *testing.T) {
	p, backend, source := captureFixture(t)
	initial, idle := captureTestToken(t, "initial"), captureTestToken(t, "idle")
	stream := &captureStream{token: initial, steps: []captureStep{{token: idle}}}
	p.openStream = func(_ context.Context, _ *mongo.Database, pipeline mongo.Pipeline, opts *options.ChangeStreamOptions) (changeStream, error) {
		require.Equal(t, int32(0), *opts.BatchSize)
		require.Nil(t, opts.ResumeAfter)
		require.Equal(t, p.buildWatchPipeline(backend.config), pipeline)
		return stream, nil
	}
	require.ErrorIs(t, p.watchChangeStream(context.Background(), backend, p.logger), context.Canceled)
	cp, err := backend.buffer.LoadCheckpoint()
	require.NoError(t, err)
	position, err := checkpoint.MatchMongoSource(cp, source)
	require.NoError(t, err)
	require.Equal(t, idle, position.ResumeAfter)
	count, err := backend.buffer.Count()
	require.NoError(t, err)
	require.Zero(t, count)
	require.True(t, stream.closed)
}

func TestCaptureFromBeginningRetainsInitialStart(t *testing.T) {
	p, backend, source := captureFixture(t)
	p.cfg.Bootstrap.Mode = "from_beginning"
	stream := &captureStream{token: captureTestToken(t, "later-batch-position")}
	p.openStream = func(_ context.Context, _ *mongo.Database, _ mongo.Pipeline, opts *options.ChangeStreamOptions) (changeStream, error) {
		require.Equal(t, &primitive.Timestamp{T: 1, I: 1}, opts.StartAtOperationTime)
		require.Nil(t, opts.BatchSize)
		return stream, nil
	}
	require.ErrorIs(t, p.watchChangeStream(context.Background(), backend, p.logger), context.Canceled)
	cp, err := backend.buffer.LoadCheckpoint()
	require.NoError(t, err)
	position, err := checkpoint.MatchMongoSource(cp, source)
	require.NoError(t, err)
	require.Equal(t, &primitive.Timestamp{T: 1, I: 1}, position.StartAt)
	require.Nil(t, position.ResumeAfter)
}

func TestCaptureRecordTokenAndReconnectAdmission(t *testing.T) {
	p, backend, source := captureFixture(t)
	initial, eventToken, batchToken := captureTestToken(t, "initial"), captureTestToken(t, "event"), captureTestToken(t, "batch")
	stream := &captureStream{token: initial, steps: []captureStep{{event: captureEvent(t, eventToken), token: batchToken}, {err: errors.New("connection reset by peer")}}}
	attachCaptureStream(p, stream)
	err := p.watchChangeStream(context.Background(), backend, p.logger)
	require.ErrorContains(t, err, "connection reset")
	durable, err := backend.buffer.LoadCheckpoint()
	require.NoError(t, err)
	position, err := checkpoint.MatchMongoSource(durable, source)
	require.NoError(t, err)
	require.Equal(t, initial, position.ResumeAfter)
	p.openStream = func(_ context.Context, _ *mongo.Database, _ mongo.Pipeline, opts *options.ChangeStreamOptions) (changeStream, error) {
		require.Equal(t, eventToken, opts.ResumeAfter)
		return &captureStream{token: eventToken}, nil
	}
	require.ErrorIs(t, p.watchChangeStream(context.Background(), backend, p.logger), context.Canceled)
	cp, err := checkpoint.EncodeMongo(source, batchToken)
	require.NoError(t, err)
	require.NoError(t, saveCaptureCheckpoint(backend, cp))
	evt, err := backend.normalizer.Normalize(captureEvent(t, eventToken))
	require.NoError(t, err)
	record, err := backend.buffer.ReadRecord(evt.BufferKey())
	require.NoError(t, err)
	require.NotNil(t, record)
	position, err = checkpoint.MatchMongoSource(record.Checkpoint, source)
	require.NoError(t, err)
	require.Equal(t, eventToken, position.ResumeAfter)
}

func TestCaptureSourceChangesAreRejectedAroundOpen(t *testing.T) {
	for _, duringOpen := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "during"}[duringOpen], func(t *testing.T) {
			p, backend, source := captureFixture(t)
			cp, err := checkpoint.EncodeMongo(source, captureTestToken(t, "saved"))
			require.NoError(t, err)
			require.NoError(t, backend.buffer.SaveCheckpoint(cp))
			changed := source
			changed.Collections = []checkpoint.MongoCollection{{Name: "users", UUID: "02020202020202020202020202020202"}}
			reads, opens := 0, 0
			p.readSource = func(context.Context, *mongo.Database, string, []string) (checkpoint.MongoSource, error) {
				reads++
				if !duringOpen || reads > 1 {
					return changed, nil
				}
				return source, nil
			}
			stream := &captureStream{}
			p.openStream = func(context.Context, *mongo.Database, mongo.Pipeline, *options.ChangeStreamOptions) (changeStream, error) {
				opens++
				return stream, nil
			}
			requireCaptureCode(t, p.watchChangeStream(context.Background(), backend, p.logger), checkpoint.SourceMismatch)
			require.Equal(t, map[bool]int{false: 0, true: 1}[duringOpen], opens)
			retained, err := backend.buffer.LoadCheckpoint()
			require.NoError(t, err)
			require.Equal(t, cp, retained)
			if duringOpen {
				require.True(t, stream.closed)
			}
		})
	}
}

func TestCaptureFailureStopsBeforeLaterPosition(t *testing.T) {
	for _, failure := range []string{"normalize", "write", "drop", "dropDatabase", "rename", "invalidate"} {
		t.Run(failure, func(t *testing.T) {
			p, backend, source := captureFixture(t)
			initial := captureTestToken(t, "initial")
			raw := captureEvent(t, captureTestToken(t, "event"))
			step := captureStep{event: raw, token: captureTestToken(t, "later")}
			code := checkpoint.SourceUnavailable
			switch failure {
			case "normalize":
				raw.DocumentKey = nil
			case "write":
				step.before = func() { require.NoError(t, backend.buffer.Close()) }
			case "drop", "dropDatabase", "rename", "invalidate":
				raw.OperationType = failure
				code = checkpoint.SourceMismatch
			}
			stream := &captureStream{token: initial, steps: []captureStep{step, {token: captureTestToken(t, "even-later")}}}
			attachCaptureStream(p, stream)
			requireCaptureCode(t, p.watchChangeStream(context.Background(), backend, p.logger), code)
			require.Equal(t, 1, stream.calls)
			position, err := checkpoint.MatchMongoSource(backend.admittedCheckpoint, source)
			require.NoError(t, err)
			require.Equal(t, initial, position.ResumeAfter)
		})
	}
}

func TestCaptureLoadFailurePreventsSourceOpen(t *testing.T) {
	p, backend, _ := captureFixture(t)
	require.NoError(t, backend.buffer.Close())
	p.readSource = func(context.Context, *mongo.Database, string, []string) (checkpoint.MongoSource, error) {
		t.Fatal("source read after checkpoint load failure")
		return checkpoint.MongoSource{}, nil
	}
	requireCaptureCode(t, p.watchChangeStream(context.Background(), backend, p.logger), checkpoint.SourceUnavailable)
}

func TestCaptureCloseErrorPreservesNativeCause(t *testing.T) {
	p, backend, _ := captureFixture(t)
	cause := errors.New("cursor close failed")
	stream := &captureStream{token: captureTestToken(t, "initial"), closeErr: cause}
	attachCaptureStream(p, stream)
	err := p.watchChangeStream(context.Background(), backend, p.logger)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, cause)
	require.ErrorIs(t, p.Stop(context.Background()), cause)
}

func TestCaptureSourceLookupFailureRetainsCause(t *testing.T) {
	p, backend, _ := captureFixture(t)
	cause := errors.New("cannot enumerate collections")
	p.readSource = func(context.Context, *mongo.Database, string, []string) (checkpoint.MongoSource, error) {
		return checkpoint.MongoSource{}, cause
	}
	err := p.watchChangeStream(context.Background(), backend, p.logger)
	requireCaptureCode(t, err, checkpoint.SourceUnavailable)
	require.ErrorIs(t, err, cause)
}

func TestCaptureFatalHistoryDoesNotReconnect(t *testing.T) {
	p, backend, source := captureFixture(t)
	cp, err := checkpoint.EncodeMongo(source, captureTestToken(t, "saved"))
	require.NoError(t, err)
	require.NoError(t, backend.buffer.SaveCheckpoint(cp))
	calls := 0
	p.openStream = func(context.Context, *mongo.Database, mongo.Pipeline, *options.ChangeStreamOptions) (changeStream, error) {
		calls++
		return nil, mongo.CommandError{Code: 286}
	}
	p.wg.Add(1)
	p.runBackend(context.Background(), backend.name, backend)
	require.Equal(t, 1, calls)
	require.Equal(t, cp, backend.admittedCheckpoint)
}

func TestCaptureIdlePositionFollowsEarlierEvent(t *testing.T) {
	p, backend, source := captureFixture(t)
	initial, eventToken, idle := captureTestToken(t, "initial"), captureTestToken(t, "event"), captureTestToken(t, "idle")
	stream := &captureStream{token: initial, steps: []captureStep{{event: captureEvent(t, eventToken), token: eventToken}, {token: idle}}}
	attachCaptureStream(p, stream)
	var event *events.StoreChangeEvent
	p.SetEventHandler(func(_ context.Context, _ string, evt *events.StoreChangeEvent) error { event = evt; return nil })
	require.ErrorIs(t, p.watchChangeStream(context.Background(), backend, p.logger), context.Canceled)
	record, err := backend.buffer.ReadRecord(event.BufferKey())
	require.NoError(t, err)
	require.NotNil(t, record)
	cp, err := backend.buffer.LoadCheckpoint()
	require.NoError(t, err)
	position, err := checkpoint.MatchMongoSource(cp, source)
	require.NoError(t, err)
	require.Equal(t, idle, position.ResumeAfter)
	position, err = checkpoint.MatchMongoSource(record.Checkpoint, source)
	require.NoError(t, err)
	require.Equal(t, eventToken, position.ResumeAfter)
}

func TestCapturePipelineRetainsScopeLifecycleControls(t *testing.T) {
	p := New(config.Config{}, nil)
	scope := []string{"users", "orders"}
	require.Equal(t, mongo.Pipeline{{{Key: "$match", Value: bson.M{"$or": bson.A{
		bson.M{"ns.coll": bson.M{"$in": scope}},
		bson.M{"operationType": bson.M{"$in": bson.A{"dropDatabase", "invalidate"}}},
		bson.M{"operationType": "rename", "to.coll": bson.M{"$in": scope}},
	}}}}}, p.buildWatchPipeline(config.PullerBackendConfig{Collections: scope}))
}

type readyCaptureStream struct {
	changeStream
	ready chan struct{}
}

func (s *readyCaptureStream) TryNext(ctx context.Context) bool {
	if s.ready != nil {
		close(s.ready)
		s.ready = nil
	}
	return s.changeStream.TryNext(ctx)
}

func TestCaptureDatabaseDropStopsNativeStream(t *testing.T) {
	env := setupTestEnv(t)
	p := New(newTestConfig(t), nil)
	require.NoError(t, p.AddBackend("source", env.Client, env.DBName, config.PullerBackendConfig{Collections: []string{"users"}}))
	defer p.Stop(context.Background())
	backend := p.backends["source"]
	ready := make(chan struct{})
	p.openStream = func(ctx context.Context, db *mongo.Database, pipeline mongo.Pipeline, opts *options.ChangeStreamOptions) (changeStream, error) {
		stream, err := openMongoChangeStream(ctx, db, pipeline, opts)
		if err != nil {
			return nil, err
		}
		return &readyCaptureStream{changeStream: stream, ready: ready}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.watchChangeStream(ctx, backend, p.logger) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("capture failed before initialization: %v", err)
	case <-ctx.Done():
		t.Fatal("capture did not initialize")
	}
	initial, err := backend.buffer.LoadCheckpoint()
	require.NoError(t, err)
	require.NotEmpty(t, initial)
	require.NoError(t, env.DB.Drop(ctx))
	select {
	case err := <-done:
		requireCaptureCode(t, err, checkpoint.SourceMismatch)
	case <-ctx.Done():
		t.Fatal("capture did not stop after database drop")
	}
	retained, err := backend.buffer.LoadCheckpoint()
	require.NoError(t, err)
	require.NotEmpty(t, retained)
	source, _, err := checkpoint.DecodeMongo(initial)
	require.NoError(t, err)
	_, err = checkpoint.MatchMongoSource(retained, source)
	require.NoError(t, err)
	count, err := backend.buffer.Count()
	require.NoError(t, err)
	require.Zero(t, count)
}
