package oteldisgo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

func mustUnmarshalInteraction(t *testing.T, payload map[string]any) discord.Interaction {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal interaction payload: %v", err)
	}
	interaction, err := discord.UnmarshalInteraction(raw)
	if err != nil {
		t.Fatalf("failed to unmarshal interaction: %v", err)
	}
	return interaction
}

func newInteractionEvent(t *testing.T, payload map[string]any) *handler.InteractionEvent {
	t.Helper()
	interaction := mustUnmarshalInteraction(t, payload)
	return &handler.InteractionEvent{
		InteractionCreate: &events.InteractionCreate{
			Interaction: interaction,
		},
		Ctx:  context.Background(),
		Vars: make(map[string]string),
	}
}

func basePayload(interactionType discord.InteractionType, data map[string]any) map[string]any {
	return map[string]any{
		"id":             "100000000000000001",
		"application_id": "200000000000000002",
		"type":           interactionType,
		"token":          "test-token",
		"version":        1,
		"guild_id":       "300000000000000003",
		"channel": map[string]any{
			"id":          "400000000000000004",
			"type":        discord.ChannelTypeGuildText,
			"name":        "general",
			"permissions": "0",
		},
		"user": map[string]any{
			"id":            "500000000000000005",
			"username":      "testuser",
			"discriminator": "0",
		},
		"data": data,
	}
}

func attrMap(attrs []attribute.KeyValue) map[string]attribute.Value {
	m := make(map[string]attribute.Value, len(attrs))
	for _, kv := range attrs {
		m[string(kv.Key)] = kv.Value
	}
	return m
}

func TestSlashCommandSpanAndAttributes(t *testing.T) {
	exporter, tp := setupTestTracer(t)

	r := handler.New()
	r.Use(Middleware(WithTracerProvider(tp)))

	called := false
	r.SlashCommand("/settings/notifications/enable", func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		called = true
		if !oteltrace.SpanFromContext(e.Ctx).SpanContext().IsValid() {
			t.Errorf("expected valid span in CommandEvent.Ctx")
		}
		return nil
	})

	ie := newInteractionEvent(t, basePayload(discord.InteractionTypeApplicationCommand, map[string]any{
		"id":       "600000000000000006",
		"name":     "settings",
		"type":     discord.ApplicationCommandTypeSlash,
		"guild_id": "300000000000000003",
		"options": []map[string]any{
			{
				"name": "notifications",
				"type": discord.ApplicationCommandOptionTypeSubCommandGroup,
				"options": []map[string]any{
					{
						"name": "enable",
						"type": discord.ApplicationCommandOptionTypeSubCommand,
					},
				},
			},
		},
	}))

	if err := r.Handle("/settings/notifications/enable", ie); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatalf("expected slash command handler to be called")
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := spans[0]
	if span.Name != "SlashCommand: /settings/notifications/enable" {
		t.Errorf("unexpected span name: %q", span.Name)
	}
	if span.SpanKind != oteltrace.SpanKindServer {
		t.Errorf("expected SpanKindServer, got %v", span.SpanKind)
	}
	if span.Status.Code != codes.Unset {
		t.Errorf("expected status Unset, got %v", span.Status.Code)
	}
	if span.InstrumentationScope.Name != Module {
		t.Errorf("InstrumentationScope.Name = %q, want %q", span.InstrumentationScope.Name, Module)
	}

	attrs := attrMap(span.Attributes)
	expectedStrings := map[string]string{
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
	}
	for k, want := range expectedStrings {
		got, ok := attrs[k]
		if !ok {
			t.Errorf("missing expected attribute %q", k)
			continue
		}
		if got.AsString() != want {
			t.Errorf("attribute %q = %q, want %q", k, got.AsString(), want)
		}
	}
	if got := attrs["interaction.type"].AsInt64(); got != int64(discord.InteractionTypeApplicationCommand) {
		t.Errorf("interaction.type = %d, want %d", got, discord.InteractionTypeApplicationCommand)
	}
}

func TestOtherCommandAndAutocompleteSpans(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		payload      map[string]any
		register     func(r *handler.Mux)
		wantSpanName string
		wantAttrKey  string
		wantAttrVal  string
	}{
		{
			name: "UserCommand",
			path: "/User Info",
			payload: basePayload(discord.InteractionTypeApplicationCommand, map[string]any{
				"id":        "600000000000000006",
				"name":      "User Info",
				"type":      discord.ApplicationCommandTypeUser,
				"target_id": "700000000000000007",
			}),
			register: func(r *handler.Mux) {
				r.UserCommand("/User Info", func(data discord.UserCommandInteractionData, e *handler.CommandEvent) error {
					return nil
				})
			},
			wantSpanName: "UserCommand: /User Info",
			wantAttrKey:  "interaction.command.user.id",
			wantAttrVal:  "700000000000000007",
		},
		{
			name: "MessageCommand",
			path: "/Bookmark",
			payload: basePayload(discord.InteractionTypeApplicationCommand, map[string]any{
				"id":        "600000000000000006",
				"name":      "Bookmark",
				"type":      discord.ApplicationCommandTypeMessage,
				"target_id": "800000000000000008",
			}),
			register: func(r *handler.Mux) {
				r.MessageCommand("/Bookmark", func(data discord.MessageCommandInteractionData, e *handler.CommandEvent) error {
					return nil
				})
			},
			wantSpanName: "MessageCommand: /Bookmark",
			wantAttrKey:  "interaction.command.message.id",
			wantAttrVal:  "800000000000000008",
		},
		{
			name: "EntryPointCommand",
			path: "/launch",
			payload: basePayload(discord.InteractionTypeApplicationCommand, map[string]any{
				"id":   "600000000000000006",
				"name": "launch",
				"type": discord.ApplicationCommandTypePrimaryEntryPoint,
			}),
			register: func(r *handler.Mux) {
				r.EntryPointCommand("/launch", func(data discord.EntryPointCommandInteractionData, e *handler.CommandEvent) error {
					return nil
				})
			},
			wantSpanName: "EntryPointCommand: /launch",
			wantAttrKey:  "interaction.command.name",
			wantAttrVal:  "launch",
		},
		{
			name: "Autocomplete",
			path: "/search/catalog/items",
			payload: basePayload(discord.InteractionTypeAutocomplete, map[string]any{
				"id":       "600000000000000006",
				"name":     "search",
				"guild_id": "300000000000000003",
				"options": []map[string]any{
					{
						"name": "catalog",
						"type": discord.ApplicationCommandOptionTypeSubCommandGroup,
						"options": []map[string]any{
							{
								"name": "items",
								"type": discord.ApplicationCommandOptionTypeSubCommand,
							},
						},
					},
				},
			}),
			register: func(r *handler.Mux) {
				r.Autocomplete("/search/catalog/items", func(e *handler.AutocompleteEvent) error {
					return nil
				})
			},
			wantSpanName: "Autocomplete: /search/catalog/items",
			wantAttrKey:  "interaction.command.path",
			wantAttrVal:  "/search/catalog/items",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exporter, tp := setupTestTracer(t)
			r := handler.New()
			r.Use(Middleware(WithTracerProvider(tp)))
			tc.register(r)

			ie := newInteractionEvent(t, tc.payload)
			if err := r.Handle(tc.path, ie); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			spans := exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("expected 1 span, got %d", len(spans))
			}
			if spans[0].Name != tc.wantSpanName {
				t.Errorf("span.Name = %q, want %q", spans[0].Name, tc.wantSpanName)
			}
			attrs := attrMap(spans[0].Attributes)
			if got := attrs[tc.wantAttrKey].AsString(); got != tc.wantAttrVal {
				t.Errorf("attr[%q] = %q, want %q", tc.wantAttrKey, got, tc.wantAttrVal)
			}
		})
	}
}

func TestFilterSkipsSpanAndMetricsWhenFalse(t *testing.T) {
	exporter, tp := setupTestTracer(t)
	reader, mp := setupTestMeter(t)

	mw := Middleware(
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

	ie := newInteractionEvent(t, basePayload(discord.InteractionTypeApplicationCommand, map[string]any{
		"id":   "600000000000000006",
		"name": "ping",
		"type": discord.ApplicationCommandTypeSlash,
	}))

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

	mw := Middleware(WithTracerProvider(tp))
	expectedErr := errors.New("interaction failed: database timeout")

	wrapped := mw(func(e *handler.InteractionEvent) error {
		return expectedErr
	})

	ie := newInteractionEvent(t, basePayload(discord.InteractionTypeApplicationCommand, map[string]any{
		"id":   "600000000000000006",
		"name": "ping",
		"type": discord.ApplicationCommandTypeSlash,
	}))

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

func TestComponentAndModalRouteNormalization(t *testing.T) {
	exporter, tp := setupTestTracer(t)

	r := handler.New()
	r.Use(Middleware(WithTracerProvider(tp)))

	r.ButtonComponent("/rps/play/{choice}/{game_id}", func(data discord.ButtonInteractionData, e *handler.ComponentEvent) error {
		return nil
	})
	r.ButtonComponent("/game/{mode}/{id}", func(data discord.ButtonInteractionData, e *handler.ComponentEvent) error {
		return nil
	})
	r.SelectMenuComponent("/poll/vote/{poll_id}", func(data discord.SelectMenuInteractionData, e *handler.ComponentEvent) error {
		return nil
	})
	r.Modal("/feedback/submit/{ticket_id}", func(e *handler.ModalEvent) error {
		return nil
	})

	rawBtnID := "/rps/play/rock/rps-747403406154399765-1729771800000"
	btnEvent := newInteractionEvent(t, basePayload(discord.InteractionTypeComponent, map[string]any{
		"component_type": discord.ComponentTypeButton,
		"custom_id":      rawBtnID,
	}))
	if err := r.Handle(rawBtnID, btnEvent); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	collisionBtnID := "/game/game/999"
	collisionEvent := newInteractionEvent(t, basePayload(discord.InteractionTypeComponent, map[string]any{
		"component_type": discord.ComponentTypeButton,
		"custom_id":      collisionBtnID,
	}))
	if err := r.Handle(collisionBtnID, collisionEvent); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rawMenuID := "/poll/vote/poll-42"
	menuEvent := newInteractionEvent(t, basePayload(discord.InteractionTypeComponent, map[string]any{
		"component_type": discord.ComponentTypeStringSelectMenu,
		"custom_id":      rawMenuID,
		"values":         []string{"opt1"},
	}))
	if err := r.Handle(rawMenuID, menuEvent); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rawModalID := "/feedback/submit/ticket-888"
	modalEvent := newInteractionEvent(t, basePayload(discord.InteractionTypeModalSubmit, map[string]any{
		"custom_id":  rawModalID,
		"components": []any{},
	}))
	if err := r.Handle(rawModalID, modalEvent); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 4 {
		t.Fatalf("expected 4 spans, got %d", len(spans))
	}

	if spans[0].Name != "Button: /rps/play/{choice}/{game_id}" {
		t.Errorf("span[0].Name = %q, want %q", spans[0].Name, "Button: /rps/play/{choice}/{game_id}")
	}
	if got := attrMap(spans[0].Attributes)["interaction.component.customid"].AsString(); got != rawBtnID {
		t.Errorf("expected raw custom_id attribute %q, got %q", rawBtnID, got)
	}

	if spans[1].Name != "Button: /game/{mode}/{id}" {
		t.Errorf("span[1].Name = %q, want %q", spans[1].Name, "Button: /game/{mode}/{id}")
	}

	if spans[2].Name != "SelectMenu: /poll/vote/{poll_id}" {
		t.Errorf("span[2].Name = %q, want %q", spans[2].Name, "SelectMenu: /poll/vote/{poll_id}")
	}

	if spans[3].Name != "ModalSubmit: /feedback/submit/{ticket_id}" {
		t.Errorf("span[3].Name = %q, want %q", spans[3].Name, "ModalSubmit: /feedback/submit/{ticket_id}")
	}
	if got := attrMap(spans[3].Attributes)["interaction.modal.customid"].AsString(); got != rawModalID {
		t.Errorf("expected raw modal custom_id attribute %q, got %q", rawModalID, got)
	}
}

func TestWithSpanNameFormatter(t *testing.T) {
	exporter, tp := setupTestTracer(t)

	r := handler.New()
	r.Use(Middleware(
		WithTracerProvider(tp),
		WithSpanNameFormatter(func(e *handler.InteractionEvent) string {
			return "custom:" + e.Vars["action"]
		}),
	))

	r.ButtonComponent("/item/{action}", func(data discord.ButtonInteractionData, e *handler.ComponentEvent) error {
		return nil
	})

	ie := newInteractionEvent(t, basePayload(discord.InteractionTypeComponent, map[string]any{
		"component_type": discord.ComponentTypeButton,
		"custom_id":      "/item/archive",
	}))
	if err := r.Handle("/item/archive", ie); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Name != "custom:archive" {
		t.Errorf("span.Name = %q, want %q", spans[0].Name, "custom:archive")
	}
}

func TestMetricsRecordingAndCardinality(t *testing.T) {
	_, tp := setupTestTracer(t)
	reader, mp := setupTestMeter(t)

	r := handler.New()
	r.Use(Middleware(WithTracerProvider(tp), WithMeterProvider(mp)))

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

	r.SlashCommand("/fail", func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		return errors.New("boom")
	})

	btnEvent := newInteractionEvent(t, basePayload(discord.InteractionTypeComponent, map[string]any{
		"component_type": discord.ComponentTypeButton,
		"custom_id":      "/rps/play/rock/rps-999",
	}))
	if err := r.Handle("/rps/play/rock/rps-999", btnEvent); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if activeDuringHandler != 1 {
		t.Errorf("expected active interactions to be 1 during handler execution, got %d", activeDuringHandler)
	}

	failEvent := newInteractionEvent(t, basePayload(discord.InteractionTypeApplicationCommand, map[string]any{
		"id":   "600000000000000006",
		"name": "fail",
		"type": discord.ApplicationCommandTypeSlash,
	}))
	_ = r.Handle("/fail", failEvent)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}
	if len(rm.ScopeMetrics) != 1 {
		t.Fatalf("expected 1 ScopeMetrics, got %d", len(rm.ScopeMetrics))
	}
	if rm.ScopeMetrics[0].Scope.Name != Module {
		t.Errorf("ScopeMetrics.Scope.Name = %q, want %q", rm.ScopeMetrics[0].Scope.Name, Module)
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
		case "SlashCommand: /fail":
			if status != "error" {
				t.Errorf("expected status error for /fail, got %q", status)
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

func TestNilSafety(t *testing.T) {
	exporter, tp := setupTestTracer(t)

	cfg := defaultConfig()
	cfg.apply([]ConfigOpt{WithTracerProvider(nil), WithMeterProvider(nil), WithSpanNameFormatter(nil)})
	if cfg.TracerProvider == nil || cfg.MeterProvider == nil || cfg.SpanNameFormatter == nil {
		t.Fatalf("expected non-nil providers and formatter when nil passed to config options")
	}

	mw := Middleware(WithTracerProvider(tp))
	wrapped := mw(func(e *handler.InteractionEvent) error {
		return nil
	})

	payload := map[string]any{
		"id":             "100000000000000001",
		"application_id": "200000000000000002",
		"type":           discord.InteractionTypeApplicationCommand,
		"token":          "test-token",
		"version":        1,
		"user": map[string]any{
			"id":            "500000000000000005",
			"username":      "testuser",
			"discriminator": "0",
		},
		"data": map[string]any{
			"id":   "600000000000000006",
			"name": "launch",
			"type": discord.ApplicationCommandTypePrimaryEntryPoint,
		},
	}
	ie := newInteractionEvent(t, payload)

	if err := wrapped(ie); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	attrs := attrMap(spans[0].Attributes)
	if _, ok := attrs["interaction.channel.id"]; ok {
		t.Errorf("did not expect interaction.channel.id when channel is absent")
	}
	if _, ok := attrs["interaction.guild.id"]; ok {
		t.Errorf("did not expect interaction.guild.id when guild_id is absent")
	}

	// PingInteraction hits the default "Unknown" branch
	pingEvent := newInteractionEvent(t, map[string]any{
		"id":             "100000000000000001",
		"application_id": "200000000000000002",
		"type":           discord.InteractionTypePing,
		"token":          "test-token",
		"version":        1,
		"user": map[string]any{
			"id":            "500000000000000005",
			"username":      "testuser",
			"discriminator": "0",
		},
	})
	if err := wrapped(pingEvent); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spans = exporter.GetSpans()
	if len(spans) != 2 || spans[1].Name != "Unknown" {
		t.Errorf("expected second span name %q, got %+v", "Unknown", spans)
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

type roundTripperFunc func(r *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type closeIdleRoundTripper struct {
	http.RoundTripper
	closed bool
}

func (c *closeIdleRoundTripper) CloseIdleConnections() {
	c.closed = true
}

func TestNewTransportRedactsTokens(t *testing.T) {
	// Verify nil base defaults to http.DefaultTransport without panicking
	if tr := NewTransport(nil); tr == nil {
		t.Fatalf("expected non-nil RoundTripper when base is nil")
	}

	// Verify CloseIdleConnections forwards to base transport
	baseIdle := &closeIdleRoundTripper{RoundTripper: http.DefaultTransport}
	trIdle := NewTransport(baseIdle)
	trIdle.(interface{ CloseIdleConnections() }).CloseIdleConnections()
	if !baseIdle.closed {
		t.Errorf("expected CloseIdleConnections to be forwarded to base RoundTripper")
	}

	tests := []struct {
		name        string
		rawURL      string
		wantURLFull string
	}{
		{
			name:        "interaction callback token redacted",
			rawURL:      "https://discord.com/api/v10/interactions/123456789/secret-interaction-token/callback",
			wantURLFull: "https://discord.com/api/v10/interactions/123456789/REDACTED/callback",
		},
		{
			name:        "interaction original response token redacted",
			rawURL:      "https://discord.com/api/v10/webhooks/200000002/secret-interaction-token/messages/@original",
			wantURLFull: "https://discord.com/api/v10/webhooks/200000002/REDACTED/messages/@original",
		},
		{
			name:        "webhook execute token with query redacted",
			rawURL:      "https://discord.com/api/v10/webhooks/200000002/secret-webhook-token?wait=true",
			wantURLFull: "https://discord.com/api/v10/webhooks/200000002/REDACTED?wait=true",
		},
		{
			name:        "webhook without token untouched",
			rawURL:      "https://discord.com/api/v10/webhooks/200000002",
			wantURLFull: "https://discord.com/api/v10/webhooks/200000002",
		},
		{
			name:        "regular channel messages endpoint untouched",
			rawURL:      "https://discord.com/api/v10/channels/400000004/messages",
			wantURLFull: "https://discord.com/api/v10/channels/400000004/messages",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exporter, tp := setupTestTracer(t)
			tracer := tp.Tracer("test")

			var receivedURL string
			rt := NewTransport(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				receivedURL = r.URL.String()
				return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
			}))

			ctx, span := tracer.Start(context.Background(), "HTTP POST",
				oteltrace.WithAttributes(attribute.String("url.full", tc.rawURL)),
			)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, tc.rawURL, nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}

			resp, err := rt.RoundTrip(req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			_ = resp.Body.Close()
			span.End()

			if receivedURL != tc.rawURL {
				t.Errorf("underlying RoundTripper received URL %q, want unredacted %q", receivedURL, tc.rawURL)
			}

			spans := exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("expected 1 span, got %d", len(spans))
			}
			gotURLFull := attrMap(spans[0].Attributes)["url.full"].AsString()
			if gotURLFull != tc.wantURLFull {
				t.Errorf("span url.full = %q, want %q", gotURLFull, tc.wantURLFull)
			}
		})
	}
}
