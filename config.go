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

func defaultConfig() config {
	return config{
		TracerProvider:    otel.GetTracerProvider(),
		MeterProvider:     otel.GetMeterProvider(),
		Filter:            nil,
		SpanNameFormatter: defaultSpanNameFormatter,
	}
}

type config struct {
	TracerProvider    oteltrace.TracerProvider
	MeterProvider     otelmetric.MeterProvider
	Filter            FilterFunc
	SpanNameFormatter SpanNameFormatter
}

type ConfigOpt func(config *config)

func (c *config) apply(opts []ConfigOpt) {
	for _, opt := range opts {
		opt(c)
	}
}

func WithTracerProvider(provider oteltrace.TracerProvider) ConfigOpt {
	return func(cfg *config) {
		if provider == nil {
			provider = otel.GetTracerProvider()
		}
		cfg.TracerProvider = provider
	}
}

func WithMeterProvider(provider otelmetric.MeterProvider) ConfigOpt {
	return func(cfg *config) {
		if provider == nil {
			provider = otel.GetMeterProvider()
		}
		cfg.MeterProvider = provider
	}
}

func WithFilter(filter FilterFunc) ConfigOpt {
	return func(cfg *config) {
		cfg.Filter = filter
	}
}

func WithSpanNameFormatter(formatter SpanNameFormatter) ConfigOpt {
	return func(cfg *config) {
		if formatter == nil {
			formatter = defaultSpanNameFormatter
		}
		cfg.SpanNameFormatter = formatter
	}
}
