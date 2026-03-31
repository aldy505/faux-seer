// Package observability configures Sentry error monitoring, tracing, logs, and metrics.
package observability

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"
	sentryhttp "github.com/getsentry/sentry-go/http"
	sentryslog "github.com/getsentry/sentry-go/slog"

	"github.com/aldy505/faux-seer/internal/config"
)

// Setup contains configured observability primitives.
type Setup struct {
	Logger   *slog.Logger
	WrapHTTP func(http.Handler) http.Handler
	Flush    func(time.Duration) bool
	Enabled  bool
}

// Initialize configures Sentry and returns wrapped logging and HTTP helpers.
func Initialize(ctx context.Context, cfg *config.Config) (*Setup, error) {
	baseHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)})

	if err := sentry.Init(sentry.ClientOptions{
		Dsn:              cfg.SentryDSN,
		SampleRate:       cfg.SentrySampleRate,
		SendDefaultPII:   cfg.SentrySendDefaultPII,
		AttachStacktrace: true,
		EnableTracing:    true,
		TracesSampleRate: cfg.SentryTracesRate,
		EnableLogs:       true,
		Debug:            cfg.LogLevel == "debug",
	}); err != nil {
		return nil, err
	}

	logHandler := sentryslog.Option{
		EventLevel: []slog.Level{slog.LevelError},
		LogLevel:   []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError},
		AddSource:  cfg.LogLevel == "debug",
	}.NewSentryHandler(ctx)

	meter := sentry.NewMeter(ctx)
	httpMiddleware := sentryhttp.New(sentryhttp.Options{
		Repanic:         true,
		WaitForDelivery: false,
		Timeout:         2 * time.Second,
	})

	return &Setup{
		Logger: slog.New(multiHandler{handlers: []slog.Handler{baseHandler, logHandler}}),
		WrapHTTP: func(next http.Handler) http.Handler {
			return metricMiddleware(meter, httpMiddleware.Handle(next))
		},
		Flush:   sentry.Flush,
		Enabled: cfg.SentryDSN != "",
	}, nil
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type multiHandler struct {
	handlers []slog.Handler
}

func (m multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range m.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m multiHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, handler := range m.handlers {
		if handler.Enabled(ctx, record.Level) {
			if err := handler.Handle(ctx, record); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, 0, len(m.handlers))
	for _, handler := range m.handlers {
		next = append(next, handler.WithAttrs(attrs))
	}
	return multiHandler{handlers: next}
}

func (m multiHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, 0, len(m.handlers))
	for _, handler := range m.handlers {
		next = append(next, handler.WithGroup(name))
	}
	return multiHandler{handlers: next}
}

func metricMiddleware(meter sentry.Meter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		durationMs := float64(time.Since(start).Milliseconds())
		meter.WithCtx(r.Context()).Count(
			"http.server.request.count",
			1,
			sentry.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.route", r.URL.Path),
				attribute.Int("http.status_code", recorder.status),
			),
		)
		meter.WithCtx(r.Context()).Distribution(
			"http.server.request.duration",
			durationMs,
			sentry.WithUnit(sentry.UnitMillisecond),
			sentry.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.route", r.URL.Path),
				attribute.Int("http.status_code", recorder.status),
			),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}
