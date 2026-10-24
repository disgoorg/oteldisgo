package oteldisgo

import (
	"github.com/disgoorg/disgo/handler"
	"go.opentelemetry.io/otel"
	otelmetric "go.opentelemetry.io/otel/metric"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type (
	FilterFunc        func(e *handler.InteractionEvent) bool
	SpanNameFormatter func(e *handler.InteractionEvent) string
)

func DefaultConfig() *Config {
	return &Config{
		TracerProvider:    otel.GetTracerProvider(),
		MeterProvider:     otel.GetMeterProvider(),
		Filter:            nil,
		SpanNameFormatter: nil,
	}
}

type Config struct {
	TracerProvider    oteltrace.TracerProvider
	MeterProvider     otelmetric.MeterProvider
	Filter            FilterFunc
	SpanNameFormatter SpanNameFormatter
}

type ConfigOpt func(config *Config)

func (c *Config) Apply(opts []ConfigOpt) {
	for _, opt := range opts {
		opt(c)
	}
}

func WithTracerProvider(provider oteltrace.TracerProvider) ConfigOpt {
	return func(cfg *Config) {
		if provider != nil {
			cfg.TracerProvider = provider
		}
	}
}

func WithMeterProvider(provider otelmetric.MeterProvider) ConfigOpt {
	return func(cfg *Config) {
		if provider != nil {
			cfg.MeterProvider = provider
		}
	}
}

func WithFilter(filter FilterFunc) ConfigOpt {
	return func(cfg *Config) {
		cfg.Filter = filter
	}
}

func WithSpanNameFormatter(formatter SpanNameFormatter) ConfigOpt {
	return func(cfg *Config) {
		cfg.SpanNameFormatter = formatter
	}
}
