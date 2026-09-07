package core

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/puller/buffer"
	"github.com/syntrixbase/syntrix/internal/puller/config"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"github.com/syntrixbase/syntrix/internal/puller/health"
	"github.com/syntrixbase/syntrix/internal/puller/normalizer"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type sourceTestStream struct {
	next   func(context.Context) bool
	decode func(any) error
	err    error
	close  func(context.Context) error
}

func (s *sourceTestStream) Next(ctx context.Context) bool {
	if s.next != nil {
		return s.next(ctx)
	}
	<-ctx.Done()
	return false
}

func (s *sourceTestStream) Decode(target any) error { return s.decode(target) }
func (s *sourceTestStream) Err() error              { return s.err }
func (s *sourceTestStream) Close(ctx context.Context) error {
	if s.close != nil {
		return s.close(ctx)
	}
	return nil
}

func sourceTestRaw(t *testing.T) normalizer.RawEvent {
	t.Helper()
	token, err := bson.Marshal(bson.D{{Key: "token", Value: "source-event-1"}})
	require.NoError(t, err)
	raw := normalizer.RawEvent{OperationType: "insert", ClusterTime: primitive.Timestamp{T: 100, I: 2},
		DocumentKey: bson.M{"_id": "one"}, ResumeToken: token}
	raw.Namespace.DB = "test"
	raw.Namespace.Coll = "documents"
	return raw
}

func TestPullerStartWaitsForOpenedStream(t *testing.T) {
	p := newTestPuller(t, newTestConfig(t), "primary")
	opening := make(chan struct{})
	release := make(chan struct{})
	p.openStream = func(ctx context.Context, _ *mongo.Database, _ mongo.Pipeline, _ *options.ChangeStreamOptions) (changeStream, error) {
		close(opening)
		select {
		case <-release:
			return &sourceTestStream{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ctx := testContext(t)
	started := make(chan error, 1)
	go func() { started <- p.Start(ctx) }()
	select {
	case <-opening:
	case <-ctx.Done():
		t.Fatal("source opening did not begin")
	}
	select {
	case err := <-started:
		t.Fatalf("Start returned before the source opened: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-started:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("Start did not acknowledge the opened stream")
	}
	require.ErrorContains(t, p.Start(ctx), "already started or stopped")
	require.NoError(t, p.Stop(testContext(t)))
}

func TestPullerStartReportsAnyBackendFailure(t *testing.T) {
	p := newTestPuller(t, newTestConfig(t), "retrying", "fatal")
	p.SetRetryDelay(time.Millisecond)
	var attempts atomic.Int32
	retried := make(chan struct{})
	failure := errors.New("source configuration rejected")
	p.watchFunc = func(ctx context.Context, backend *Backend, _ *slog.Logger) error {
		if backend.name == "retrying" {
			if attempts.Add(1) == 2 {
				close(retried)
			}
			return io.EOF
		}
		select {
		case <-retried:
			return failure
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	err := p.Start(testContext(t))
	require.ErrorIs(t, err, failure)
	var domain *events.Error
	require.ErrorAs(t, err, &domain)
	assert.Equal(t, events.CodeSourceUnavailable, domain.Code)
	assert.GreaterOrEqual(t, attempts.Load(), int32(2))
	require.ErrorIs(t, p.Stop(testContext(t)), failure)
	assert.Equal(t, health.StatusUnhealthy, p.HealthReport().Status)
}

func TestPullerLifetimeCancellationClosesIndependentSubscription(t *testing.T) {
	p := newTestPuller(t, newTestConfig(t), "primary")
	p.openStream = func(context.Context, *mongo.Database, mongo.Pipeline, *options.ChangeStreamOptions) (changeStream, error) {
		return &sourceTestStream{}, nil
	}
	sub, _ := initialSubscription(t, p, events.SubscribeOptions{})
	lifetime, cancel := context.WithCancel(testContext(t))
	t.Cleanup(cancel)
	require.NoError(t, p.Start(lifetime))
	cancel()
	_, err := sub.Next(testContext(t))
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, p.Stop(testContext(t)))
	assert.Equal(t, health.StatusUnhealthy, p.HealthReport().Status)
	_, err = p.Subscribe(testContext(t), events.SubscribeOptions{})
	require.ErrorIs(t, err, context.Canceled)
}

func TestPullerSourceFailuresRemainTypedAndTerminal(t *testing.T) {
	decodeFailure := errors.New("malformed change stream document")
	openFailure := errors.New("change stream opening rejected")
	closeFailure := errors.New("change stream close failed")
	historyFailure := &mongo.CommandError{Code: 286, Message: "resume history no longer retained"}
	for _, tc := range []struct {
		name      string
		code      events.ErrorCode
		cause     error
		message   string
		configure func(*Puller)
	}{
		{"open", events.CodeSourceUnavailable, openFailure, "open change stream", func(p *Puller) {
			p.openStream = func(context.Context, *mongo.Database, mongo.Pipeline, *options.ChangeStreamOptions) (changeStream, error) {
				return nil, openFailure
			}
		}},
		{"decode", events.CodeSourceUnavailable, decodeFailure, "decode source event", func(p *Puller) {
			p.openStream = func(context.Context, *mongo.Database, mongo.Pipeline, *options.ChangeStreamOptions) (changeStream, error) {
				return &sourceTestStream{next: func(context.Context) bool { return true }, decode: func(any) error { return decodeFailure }}, nil
			}
		}},
		{"normalize", events.CodeSourceUnavailable, nil, "normalize source event", func(p *Puller) {
			p.openStream = func(context.Context, *mongo.Database, mongo.Pipeline, *options.ChangeStreamOptions) (changeStream, error) {
				return &sourceTestStream{next: func(context.Context) bool { return true }, decode: func(target any) error {
					*target.(*normalizer.RawEvent) = normalizer.RawEvent{OperationType: "invalid"}
					return nil
				}}, nil
			}
		}},
		{"history", events.CodeContinuityLost, historyFailure, "resume history", func(p *Puller) {
			p.openStream = func(context.Context, *mongo.Database, mongo.Pipeline, *options.ChangeStreamOptions) (changeStream, error) {
				return &sourceTestStream{next: func(context.Context) bool { return false }, err: historyFailure}, nil
			}
		}},
		{"close", events.CodeSourceUnavailable, closeFailure, "change stream close failed", func(p *Puller) {
			p.openStream = func(context.Context, *mongo.Database, mongo.Pipeline, *options.ChangeStreamOptions) (changeStream, error) {
				return &sourceTestStream{next: func(context.Context) bool { return false }, close: func(context.Context) error { return closeFailure }}, nil
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newTestConfig(t)
			p := newTestPuller(t, cfg, "primary")
			sub, _ := initialSubscription(t, p, events.SubscribeOptions{})
			tc.configure(p)
			startErr := p.Start(testContext(t))
			if startErr != nil {
				var domain *events.Error
				require.ErrorAs(t, startErr, &domain)
				assert.Equal(t, tc.code, domain.Code)
			}
			for range 2 {
				_, err := sub.Next(testContext(t))
				var domain *events.Error
				require.ErrorAs(t, err, &domain)
				assert.Equal(t, tc.code, domain.Code)
				assert.ErrorContains(t, err, tc.message)
				if tc.cause != nil {
					assert.ErrorIs(t, err, tc.cause)
				}
			}
			require.Error(t, p.Stop(testContext(t)))
			assert.Equal(t, health.StatusUnhealthy, p.HealthReport().Status)
			if tc.code == events.CodeContinuityLost {
				p2 := New(cfg, nil)
				err := p2.AddBackend("primary", p.backends["primary"].client, "test", p.backends["primary"].config)
				var domain *events.Error
				require.ErrorAs(t, err, &domain)
				assert.Equal(t, events.CodeContinuityLost, domain.Code)
			}
		})
	}
}

func TestPullerRetryFlushesAdmittedEventsBeforeResuming(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Buffer.BatchSize = 10
	p := newTestPuller(t, cfg, "primary")
	p.SetRetryDelay(time.Millisecond)
	sub, _ := initialSubscription(t, p, events.SubscribeOptions{})
	raw := sourceTestRaw(t)
	type reopenedState struct {
		token any
		state buffer.State
		err   error
	}
	reopened := make(chan reopenedState, 1)
	var opens int
	p.openStream = func(_ context.Context, _ *mongo.Database, _ mongo.Pipeline, opts *options.ChangeStreamOptions) (changeStream, error) {
		opens++
		if opens == 1 {
			emitted := false
			return &sourceTestStream{
				next: func(context.Context) bool {
					if emitted {
						return false
					}
					emitted = true
					return true
				},
				decode: func(target any) error { *target.(*normalizer.RawEvent) = raw; return nil },
				err:    mongo.CommandError{Code: 43, Message: "cursor lost"},
			}, nil
		}
		state, err := p.backends["primary"].buffer.State()
		reopened <- reopenedState{token: opts.ResumeAfter, state: state, err: err}
		return &sourceTestStream{}, nil
	}
	require.NoError(t, p.Start(testContext(t)))
	select {
	case actual := <-reopened:
		require.NoError(t, actual.err)
		assert.Equal(t, raw.ResumeToken, actual.token)
		assert.Equal(t, raw.ResumeToken, actual.state.ResumeToken)
		assert.Equal(t, uint64(1), actual.state.Position.Sequence)
		assert.Nil(t, actual.state.StartAt)
	case <-testContext(t).Done():
		t.Fatal("source did not reopen after the resumable failure")
	}
	envelope := nextEnvelope(t, sub)
	assert.Equal(t, "one", envelope.Change.MgoDocID)
	assert.Equal(t, uint64(1), sequence(t, envelope.Progress, "primary-source"))
	require.NoError(t, p.Stop(testContext(t)))
}

func TestPullerBootstrapBoundarySurvivesRetryAndRestart(t *testing.T) {
	for _, mode := range []string{"from_now", "from_beginning"} {
		t.Run(mode, func(t *testing.T) {
			cfg := newTestConfig(t)
			cfg.Bootstrap.Mode = mode
			p := newTestPuller(t, cfg, "primary")
			expected := primitive.Timestamp{T: 123, I: 45}
			captures := 0
			capture := func(context.Context, *mongo.Client) (primitive.Timestamp, error) {
				captures++
				return expected, nil
			}
			unexpected := func(context.Context, *mongo.Client) (primitive.Timestamp, error) {
				return primitive.Timestamp{}, errors.New("wrong bootstrap timestamp source")
			}
			p.currentTimestamp, p.earliestTimestamp = capture, unexpected
			if mode == "from_beginning" {
				p.currentTimestamp, p.earliestTimestamp = unexpected, capture
			}
			var observed []primitive.Timestamp
			open := func(_ context.Context, _ *mongo.Database, _ mongo.Pipeline, opts *options.ChangeStreamOptions) (changeStream, error) {
				if opts.StartAtOperationTime == nil {
					return nil, errors.New("source was reopened without its durable boundary")
				}
				observed = append(observed, *opts.StartAtOperationTime)
				return nil, io.EOF
			}
			p.openStream = open
			for range 2 {
				require.ErrorIs(t, p.watchChangeStream(testContext(t), p.backends["primary"], p.logger), io.EOF)
			}
			require.Equal(t, 1, captures)
			require.NoError(t, p.Stop(testContext(t)))
			p2 := newTestPuller(t, cfg, "primary")
			p2.currentTimestamp, p2.earliestTimestamp = unexpected, unexpected
			p2.openStream = open
			require.ErrorIs(t, p2.watchChangeStream(testContext(t), p2.backends["primary"], p2.logger), io.EOF)
			assert.Equal(t, []primitive.Timestamp{expected, expected, expected}, observed)
		})
	}
}

func TestPullerBoundaryFailuresDoNotOpenSource(t *testing.T) {
	failure := errors.New("initial operation time unavailable")
	for _, tc := range []struct {
		name      string
		timestamp primitive.Timestamp
		failure   error
		code      events.ErrorCode
	}{
		{"capture", primitive.Timestamp{}, failure, events.CodeSourceUnavailable},
		{"invalid", primitive.Timestamp{}, nil, events.CodeStorageFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestPuller(t, newTestConfig(t), "primary")
			p.currentTimestamp = func(context.Context, *mongo.Client) (primitive.Timestamp, error) {
				return tc.timestamp, tc.failure
			}
			var opened atomic.Bool
			p.openStream = func(context.Context, *mongo.Database, mongo.Pipeline, *options.ChangeStreamOptions) (changeStream, error) {
				opened.Store(true)
				return &sourceTestStream{}, nil
			}
			err := p.Start(testContext(t))
			var domain *events.Error
			require.ErrorAs(t, err, &domain)
			assert.Equal(t, tc.code, domain.Code)
			assert.False(t, opened.Load())
			if tc.failure != nil {
				assert.ErrorIs(t, err, tc.failure)
			}
		})
	}
}

func TestPullerStopTimeoutPreservesDrainOwnership(t *testing.T) {
	p := newTestPuller(t, newTestConfig(t), "primary")
	release := make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(finish)
	p.watchFunc = func(_ context.Context, backend *Backend, _ *slog.Logger) error {
		backend.readyOnce.Do(func() { close(backend.ready) })
		<-release
		return nil
	}
	require.NoError(t, p.Start(testContext(t)))
	deadline, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, p.Stop(deadline), context.DeadlineExceeded)
	_, err := p.backends["primary"].buffer.State()
	require.NoError(t, err, "the unfinished reader still owns an open buffer")
	finish()
	for range 2 {
		require.NoError(t, p.Stop(testContext(t)))
	}
	_, err = p.backends["primary"].buffer.State()
	require.ErrorContains(t, err, "closed")
}

func TestPullerConfigurationErrors(t *testing.T) {
	p := newTestPuller(t, newTestConfig(t), "primary")
	client := p.backends["primary"].client
	for _, tc := range []struct {
		name, database, source string
		client                 *mongo.Client
		message                string
	}{
		{"", "test", "new-source", client, "invalid backend name"},
		{".", "test", "new-source", client, "invalid backend name"},
		{"..", "test", "new-source", client, "invalid backend name"},
		{"../escape", "test", "new-source", client, "invalid backend name"},
		{"primary", "test", "new-source", client, "already exists"},
		{"secondary", "test", "", client, "source_id is required"},
		{"secondary", "test", "primary-source", client, "duplicate source_id"},
		{"secondary", "test", "new-source", nil, "client and database are required"},
		{"secondary", "", "new-source", client, "client and database are required"},
	} {
		t.Run(tc.message+"/"+tc.name, func(t *testing.T) {
			err := p.AddBackend(tc.name, tc.client, tc.database, config.PullerBackendConfig{SourceID: tc.source})
			require.ErrorContains(t, err, tc.message)
		})
	}
	p.cfg.Buffer.MaxSize = "invalid"
	require.ErrorContains(t, p.AddBackend("secondary", client, "test", config.PullerBackendConfig{SourceID: "new-source"}), "invalid buffer max_size")
	p.cfg.Buffer.MaxSize = "1GiB"
	sub, _ := initialSubscription(t, p, events.SubscribeOptions{})
	require.ErrorContains(t, p.AddBackend("secondary", client, "test", config.PullerBackendConfig{SourceID: "new-source"}), "before ingestion or subscriptions")
	require.NoError(t, sub.Close())
	require.Equal(t, []string{"primary"}, p.BackendNames())
	empty := newTestPuller(t, newTestConfig(t))
	require.ErrorContains(t, empty.Start(testContext(t)), "no backends configured")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, empty.Start(canceled), context.Canceled)
}

func TestPullerParseSize(t *testing.T) {
	for _, tc := range []struct {
		text string
		want int64
	}{
		{"", 0}, {"0B", 0}, {"17", 17}, {"2KB", 2 << 10}, {"3MiB", 3 << 20}, {"4 GiB", 4 << 30}, {"1TiB", 1 << 40},
	} {
		t.Run(tc.text, func(t *testing.T) {
			actual, err := parseSize(tc.text)
			require.NoError(t, err)
			assert.Equal(t, tc.want, actual)
		})
	}
	for _, invalid := range []string{"-1", "garbage", "1XB", "1.5GiB", "9223372036854775808", "9223372036854775807TiB"} {
		t.Run(invalid, func(t *testing.T) {
			_, err := parseSize(invalid)
			require.Error(t, err)
		})
	}
}
