package oteldisgo

import (
	"context"
	"embed"
	"errors"
	"path"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/handler"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func setupTestTracer(t *testing.T) (*tracetest.InMemoryExporter, oteltrace.TracerProvider) {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
	})
	return exporter, tp
}

func setupTestMeter(t *testing.T) (*sdkmetric.ManualReader, otelmetric.MeterProvider) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
	)
	t.Cleanup(func() {
		_ = mp.Shutdown(context.Background())
	})
	return reader, mp
}

//go:embed testdata/*.json
var testData embed.FS

func loadInteractionEvent(t *testing.T, file string) *handler.InteractionEvent {
	t.Helper()
	raw, err := testData.ReadFile(path.Join("testdata", file))
	if err != nil {
		t.Fatalf("failed to read testdata/%s: %v", file, err)
	}
	interaction, err := discord.UnmarshalInteraction(raw)
	if err != nil {
		t.Fatalf("failed to unmarshal testdata/%s: %v", file, err)
	}
	return &handler.InteractionEvent{
		InteractionCreate: &events.InteractionCreate{
			Interaction: interaction,
		},
		Ctx:  context.Background(),
		Vars: make(map[string]string),
	}
}

func attrMap(attrs []attribute.KeyValue) map[string]attribute.Value {
	m := make(map[string]attribute.Value, len(attrs))
	for _, kv := range attrs {
		m[string(kv.Key)] = kv.Value
	}
	return m
}

func TestInteractionSpans(t *testing.T) {
	tests := []struct {
		name         string
		file         string
		path         string
		register     func(r *handler.Mux)
		wantSpanName string
		wantStrings  map[string]string
		wantAbsent   []string
		wantType     discord.InteractionType
	}{
		{
			name: "SlashCommand",
			file: "slash_command.json",
			path: "/settings/notifications/enable",
			register: func(r *handler.Mux) {
				r.SlashCommand("/settings/notifications/enable", func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
					if !oteltrace.SpanFromContext(e.Ctx).SpanContext().IsValid() {
						t.Errorf("expected valid span in CommandEvent.Ctx")
					}
					return nil
				})
			},
			wantSpanName: "SlashCommand: /settings/notifications/enable",
			wantStrings: map[string]string{
				"interaction.id":                      "100000000000000001",
				"interaction.application.id":          "200000000000000002",
				"interaction.user.id":                 "500000000000000005",
				"interaction.channel.id":              "400000000000000004",
				"interaction.guild.id":                "300000000000000003",
				"interaction.command.id":              "600000000000000006",
				"interaction.command.name":            "settings",
				"interaction.command.guild.id":        "300000000000000003",
				"interaction.command.subcommand":      "enable",
				"interaction.command.subcommandgroup": "notifications",
				"interaction.command.path":            "/settings/notifications/enable",
			},
			wantType: discord.InteractionTypeApplicationCommand,
		},
		{
			name: "UserCommand",
			file: "user_command.json",
			path: "/User Info",
			register: func(r *handler.Mux) {
				r.UserCommand("/User Info", func(data discord.UserCommandInteractionData, e *handler.CommandEvent) error {
					return nil
				})
			},
			wantSpanName: "UserCommand: /User Info",
			wantStrings: map[string]string{
				"interaction.command.user.id": "700000000000000007",
			},
			wantType: discord.InteractionTypeApplicationCommand,
		},
		{
			name: "MessageCommand",
			file: "message_command.json",
			path: "/Bookmark",
			register: func(r *handler.Mux) {
				r.MessageCommand("/Bookmark", func(data discord.MessageCommandInteractionData, e *handler.CommandEvent) error {
					return nil
				})
			},
			wantSpanName: "MessageCommand: /Bookmark",
			wantStrings: map[string]string{
				"interaction.command.message.id": "800000000000000008",
			},
			wantType: discord.InteractionTypeApplicationCommand,
		},
		{
			name: "EntryPointCommandWithoutChannelOrGuild",
			file: "entry_point_command.json",
			path: "/launch",
			register: func(r *handler.Mux) {
				r.EntryPointCommand("/launch", func(data discord.EntryPointCommandInteractionData, e *handler.CommandEvent) error {
					return nil
				})
			},
			wantSpanName: "EntryPointCommand: /launch",
			wantStrings: map[string]string{
				"interaction.command.name": "launch",
			},
			wantAbsent: []string{"interaction.channel.id", "interaction.guild.id"},
			wantType:   discord.InteractionTypeApplicationCommand,
		},
		{
			name: "Autocomplete",
			file: "autocomplete.json",
			path: "/search/catalog/items",
			register: func(r *handler.Mux) {
				r.Autocomplete("/search/catalog/items", func(e *handler.AutocompleteEvent) error {
					return nil
				})
			},
			wantSpanName: "Autocomplete: /search/catalog/items",
			wantStrings: map[string]string{
				"interaction.command.path":            "/search/catalog/items",
				"interaction.command.subcommand":      "items",
				"interaction.command.subcommandgroup": "catalog",
			},
			wantType: discord.InteractionTypeAutocomplete,
		},
		{
			name: "ButtonComponentRouteNormalization",
			file: "button_component.json",
			path: "/rps/play/rock/rps-747403406154399765-1729771800000",
			register: func(r *handler.Mux) {
				r.ButtonComponent("/rps/play/{choice}/{game_id}", func(data discord.ButtonInteractionData, e *handler.ComponentEvent) error {
					return nil
				})
			},
			wantSpanName: "Button: /rps/play/{choice}/{game_id}",
			wantStrings: map[string]string{
				"interaction.component.customid": "/rps/play/rock/rps-747403406154399765-1729771800000",
			},
			wantType: discord.InteractionTypeComponent,
		},
		{
			name: "ButtonComponentPrefixCollision",
			file: "button_collision_component.json",
			path: "/game/game/999",
			register: func(r *handler.Mux) {
				r.ButtonComponent("/game/{mode}/{id}", func(data discord.ButtonInteractionData, e *handler.ComponentEvent) error {
					return nil
				})
			},
			wantSpanName: "Button: /game/{mode}/{id}",
			wantStrings: map[string]string{
				"interaction.component.customid": "/game/game/999",
			},
			wantType: discord.InteractionTypeComponent,
		},
		{
			name: "SelectMenuComponentRouteNormalization",
			file: "select_menu_component.json",
			path: "/poll/vote/poll-42",
			register: func(r *handler.Mux) {
				r.SelectMenuComponent("/poll/vote/{poll_id}", func(data discord.SelectMenuInteractionData, e *handler.ComponentEvent) error {
					return nil
				})
			},
			wantSpanName: "SelectMenu: /poll/vote/{poll_id}",
			wantStrings: map[string]string{
				"interaction.component.customid": "/poll/vote/poll-42",
			},
			wantType: discord.InteractionTypeComponent,
		},
		{
			name: "ModalSubmitRouteNormalization",
			file: "modal_submit.json",
			path: "/feedback/submit/ticket-888",
			register: func(r *handler.Mux) {
				r.Modal("/feedback/submit/{ticket_id}", func(e *handler.ModalEvent) error {
					return nil
				})
			},
			wantSpanName: "ModalSubmit: /feedback/submit/{ticket_id}",
			wantStrings: map[string]string{
				"interaction.modal.customid": "/feedback/submit/ticket-888",
			},
			wantType: discord.InteractionTypeModalSubmit,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exporter, tp := setupTestTracer(t)
			r := handler.New()
			r.Use(Middleware("test-bot", WithTracerProvider(tp)))
			tc.register(r)

			ie := loadInteractionEvent(t, tc.file)
			if err := r.Handle(tc.path, ie); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			spans := exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("expected 1 span, got %d", len(spans))
			}

			span := spans[0]
			if span.Name != tc.wantSpanName {
				t.Errorf("span.Name = %q, want %q", span.Name, tc.wantSpanName)
			}
			if span.SpanKind != oteltrace.SpanKindServer {
				t.Errorf("expected SpanKindServer, got %v", span.SpanKind)
			}
			if span.Status.Code != codes.Unset {
				t.Errorf("expected status Unset, got %v", span.Status.Code)
			}

			attrs := attrMap(span.Attributes)
			for k, want := range tc.wantStrings {
				got, ok := attrs[k]
				if !ok {
					t.Errorf("missing expected attribute %q", k)
					continue
				}
				if got.AsString() != want {
					t.Errorf("attribute %q = %q, want %q", k, got.AsString(), want)
				}
			}
			for _, k := range tc.wantAbsent {
				if _, ok := attrs[k]; ok {
					t.Errorf("did not expect attribute %q", k)
				}
			}
			if got := attrs["interaction.type"].AsInt64(); got != int64(tc.wantType) {
				t.Errorf("interaction.type = %d, want %d", got, tc.wantType)
			}
		})
	}
}

func TestFilterSkipsSpanAndMetricsWhenFalse(t *testing.T) {
	exporter, tp := setupTestTracer(t)
	reader, mp := setupTestMeter(t)

	mw := Middleware("test-bot",
		WithTracerProvider(tp),
		WithMeterProvider(mp),
		WithFilter(func(e *handler.InteractionEvent) bool {
			return false
		}),
	)

	called := false
	wrapped := mw(func(e *handler.InteractionEvent) error {
		called = true
		return nil
	})

	ie := loadInteractionEvent(t, "slash_command.json")
	if err := wrapped(ie); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatalf("expected downstream handler to be called even when filter returns false")
	}
	if got := len(exporter.GetSpans()); got != 0 {
		t.Fatalf("expected 0 spans when filter returns false, got %d", got)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}
	if len(rm.ScopeMetrics) != 0 {
		t.Fatalf("expected 0 metrics when filter returns false, got %+v", rm.ScopeMetrics)
	}
}

func TestHandlerErrorRecordedOnSpan(t *testing.T) {
	exporter, tp := setupTestTracer(t)

	mw := Middleware("test-bot", WithTracerProvider(tp))
	expectedErr := errors.New("interaction failed: database timeout")

	wrapped := mw(func(e *handler.InteractionEvent) error {
		return expectedErr
	})

	ie := loadInteractionEvent(t, "slash_command.json")
	err := wrapped(ie)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := spans[0]
	if span.Status.Code != codes.Error {
		t.Errorf("expected span status code Error, got %v", span.Status.Code)
	}
	if span.Status.Description != expectedErr.Error() {
		t.Errorf("expected status description %q, got %q", expectedErr.Error(), span.Status.Description)
	}
	if len(span.Events) != 1 || span.Events[0].Name != "exception" {
		t.Fatalf("expected 1 exception event on span, got %+v", span.Events)
	}
}

func TestWithSpanNameFormatter(t *testing.T) {
	exporter, tp := setupTestTracer(t)

	r := handler.New()
	r.Use(Middleware("test-bot",
		WithTracerProvider(tp),
		WithSpanNameFormatter(func(e *handler.InteractionEvent) string {
			return "custom:" + e.Vars["choice"]
		}),
	))

	r.ButtonComponent("/rps/play/{choice}/{game_id}", func(data discord.ButtonInteractionData, e *handler.ComponentEvent) error {
		return nil
	})

	ie := loadInteractionEvent(t, "button_component.json")
	if err := r.Handle("/rps/play/rock/rps-747403406154399765-1729771800000", ie); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Name != "custom:rock" {
		t.Errorf("span.Name = %q, want %q", spans[0].Name, "custom:rock")
	}
}

func TestMetricsRecordingAndCardinality(t *testing.T) {
	_, tp := setupTestTracer(t)
	reader, mp := setupTestMeter(t)

	r := handler.New()
	r.Use(Middleware("test-bot", WithTracerProvider(tp), WithMeterProvider(mp)))

	var activeDuringHandler int64
	r.ButtonComponent("/rps/play/{choice}/{game_id}", func(data discord.ButtonInteractionData, e *handler.ComponentEvent) error {
		var inFlight metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &inFlight); err != nil {
			t.Fatalf("failed to collect metrics inside handler: %v", err)
		}
		for _, sm := range inFlight.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name == "discord.interaction.active" {
					sum := m.Data.(metricdata.Sum[int64])
					if len(sum.DataPoints) == 1 {
						activeDuringHandler = sum.DataPoints[0].Value
					}
				}
			}
		}
		return nil
	})

	r.SlashCommand("/settings/notifications/enable", func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		return errors.New("boom")
	})

	btnEvent := loadInteractionEvent(t, "button_component.json")
	if err := r.Handle("/rps/play/rock/rps-747403406154399765-1729771800000", btnEvent); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if activeDuringHandler != 1 {
		t.Errorf("expected active interactions to be 1 during handler execution, got %d", activeDuringHandler)
	}

	failEvent := loadInteractionEvent(t, "slash_command.json")
	_ = r.Handle("/settings/notifications/enable", failEvent)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}
	if len(rm.ScopeMetrics) != 1 {
		t.Fatalf("expected 1 ScopeMetrics, got %d", len(rm.ScopeMetrics))
	}

	metricsByName := make(map[string]metricdata.Metrics)
	for _, m := range rm.ScopeMetrics[0].Metrics {
		metricsByName[m.Name] = m
	}

	durationMetric, ok := metricsByName["discord.interaction.duration"]
	if !ok {
		t.Fatalf("missing discord.interaction.duration metric")
	}
	hist := durationMetric.Data.(metricdata.Histogram[float64])
	if len(hist.DataPoints) != 2 {
		t.Fatalf("expected 2 histogram data points, got %d", len(hist.DataPoints))
	}

	countMetric, ok := metricsByName["discord.interaction.count"]
	if !ok {
		t.Fatalf("missing discord.interaction.count metric")
	}
	countSum := countMetric.Data.(metricdata.Sum[int64])
	if len(countSum.DataPoints) != 2 {
		t.Fatalf("expected 2 counter data points, got %d", len(countSum.DataPoints))
	}

	for _, dp := range countSum.DataPoints {
		if dp.Value != 1 {
			t.Errorf("expected counter value 1, got %d", dp.Value)
		}
		attrs := attrMap(dp.Attributes.ToSlice())
		if len(attrs) != 3 {
			t.Errorf("expected exactly 3 bounded attributes on counter, got %v", attrs)
		}
		name := attrs["interaction.name"].AsString()
		status := attrs["interaction.status"].AsString()
		switch name {
		case "Button: /rps/play/{choice}/{game_id}":
			if status != "ok" {
				t.Errorf("expected status ok for button, got %q", status)
			}
		case "SlashCommand: /settings/notifications/enable":
			if status != "error" {
				t.Errorf("expected status error for slash command, got %q", status)
			}
		default:
			t.Errorf("unexpected metric interaction.name: %q", name)
		}
	}

	activeMetric, ok := metricsByName["discord.interaction.active"]
	if !ok {
		t.Fatalf("missing discord.interaction.active metric")
	}
	activeSum := activeMetric.Data.(metricdata.Sum[int64])
	for _, dp := range activeSum.DataPoints {
		if dp.Value != 0 {
			t.Errorf("expected active interactions to return to 0 after completion, got %d", dp.Value)
		}
		attrs := attrMap(dp.Attributes.ToSlice())
		if len(attrs) != 1 {
			t.Errorf("expected only interaction.type attribute on active gauge, got %v", attrs)
		}
	}
}

func TestConfigNilFallbackAndUnknownInteraction(t *testing.T) {
	exporter, tp := setupTestTracer(t)

	cfg := defaultConfig()
	cfg.apply([]ConfigOpt{WithTracerProvider(nil), WithMeterProvider(nil), WithSpanNameFormatter(nil)})
	if cfg.TracerProvider == nil || cfg.MeterProvider == nil || cfg.SpanNameFormatter == nil {
		t.Fatalf("expected non-nil providers and formatter when nil passed to config options")
	}

	mw := Middleware("test-bot", WithTracerProvider(tp))
	wrapped := mw(func(e *handler.InteractionEvent) error {
		return nil
	})

	pingEvent := loadInteractionEvent(t, "ping.json")
	if err := wrapped(pingEvent); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "Unknown" {
		t.Errorf("expected span name %q, got %+v", "Unknown", spans)
	}
}

func TestNormalizePathEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		path string
		vars map[string]string
		want string
	}{
		{
			name: "empty path",
			path: "",
			vars: map[string]string{"id": "1"},
			want: "",
		},
		{
			name: "root slash only",
			path: "/",
			vars: map[string]string{"id": "1"},
			want: "/",
		},
		{
			name: "relative path without leading slash",
			path: "item/42",
			vars: map[string]string{"id": "42"},
			want: "item/{id}",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizePath(tc.path, tc.vars); got != tc.want {
				t.Errorf("normalizePath(%q, %v) = %q, want %q", tc.path, tc.vars, got, tc.want)
			}
		})
	}
}
