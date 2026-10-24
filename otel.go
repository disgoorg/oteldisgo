package oteldisgo

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelmetric "go.opentelemetry.io/otel/metric"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func Middleware(serverName string, opts ...ConfigOpt) handler.Middleware {
	cfg := defaultConfig()
	cfg.apply(opts)

	tracer := cfg.TracerProvider.Tracer(
		disgo.Module,
		oteltrace.WithInstrumentationVersion(disgo.SemVersion),
	)
	meter := cfg.MeterProvider.Meter(
		disgo.Module,
		otelmetric.WithInstrumentationVersion(disgo.SemVersion),
	)

	durationHistogram, err := meter.Float64Histogram(
		"discord.interaction.duration",
		otelmetric.WithDescription("Duration of Discord interaction handling."),
		otelmetric.WithUnit("s"),
		otelmetric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10),
	)
	if err != nil {
		otel.Handle(err)
	}

	interactionCounter, err := meter.Int64Counter(
		"discord.interaction.count",
		otelmetric.WithDescription("Total number of Discord interactions handled."),
		otelmetric.WithUnit("{interaction}"),
	)
	if err != nil {
		otel.Handle(err)
	}

	activeInteractions, err := meter.Int64UpDownCounter(
		"discord.interaction.active",
		otelmetric.WithDescription("Number of currently active Discord interactions being handled."),
		otelmetric.WithUnit("{interaction}"),
	)
	if err != nil {
		otel.Handle(err)
	}

	return func(handler handler.Handler) handler.Handler {
		h := &otelHandler{
			serverName:         serverName,
			handler:            handler,
			tracer:             tracer,
			meter:              meter,
			filter:             cfg.Filter,
			spanNameFormatter:  cfg.SpanNameFormatter,
			durationHistogram:  durationHistogram,
			interactionCounter: interactionCounter,
			activeInteractions: activeInteractions,
		}
		return h.Handle
	}
}

type otelHandler struct {
	serverName         string
	handler            handler.Handler
	tracer             oteltrace.Tracer
	meter              otelmetric.Meter
	filter             FilterFunc
	spanNameFormatter  SpanNameFormatter
	durationHistogram  otelmetric.Float64Histogram
	interactionCounter otelmetric.Int64Counter
	activeInteractions otelmetric.Int64UpDownCounter
}

func (h *otelHandler) Handle(e *handler.InteractionEvent) error {
	if h.filter != nil && !h.filter(e) {
		return h.handler(e)
	}

	var attr []attribute.KeyValue
	switch i := e.Interaction.(type) {
	case discord.ApplicationCommandInteraction:
		switch d := i.Data.(type) {
		case discord.SlashCommandInteractionData:
			if d.SubCommandName != nil {
				attr = append(attr, attribute.String("interaction.command.subcommand", *d.SubCommandName))
			}
			if d.SubCommandGroupName != nil {
				attr = append(attr, attribute.String("interaction.command.subcommandgroup", *d.SubCommandGroupName))
			}
			attr = append(attr, attribute.String("interaction.command.path", d.CommandPath()))
		case discord.UserCommandInteractionData:
			attr = append(attr, attribute.String("interaction.command.user.id", d.TargetID().String()))
		case discord.MessageCommandInteractionData:
			attr = append(attr, attribute.String("interaction.command.message.id", d.TargetID().String()))
		}
		attr = append(attr,
			attribute.String("interaction.command.name", i.Data.CommandName()),
			attribute.String("interaction.command.id", i.Data.CommandID().String()),
		)
		if i.Data.GuildID() != nil {
			attr = append(attr, attribute.String("interaction.command.guild.id", i.Data.GuildID().String()))
		}
	case discord.AutocompleteInteraction:
		if i.Data.SubCommandName != nil {
			attr = append(attr, attribute.String("interaction.command.subcommand", *i.Data.SubCommandName))
		}
		if i.Data.SubCommandGroupName != nil {
			attr = append(attr, attribute.String("interaction.command.subcommandgroup", *i.Data.SubCommandGroupName))
		}
		attr = append(attr,
			attribute.String("interaction.command.path", i.Data.CommandPath()),
			attribute.String("interaction.command.name", i.Data.CommandName),
			attribute.String("interaction.command.id", i.Data.CommandID.String()),
		)
		if i.Data.GuildID != nil {
			attr = append(attr, attribute.String("interaction.command.guild.id", i.Data.GuildID.String()))
		}
	case discord.ComponentInteraction:
		attr = append(attr,
			attribute.Int("interaction.component.type", int(i.Data.Type())),
			attribute.String("interaction.component.customid", i.Data.CustomID()),
		)
	case discord.ModalSubmitInteraction:
		attr = append(attr,
			attribute.String("interaction.modal.customid", i.Data.CustomID),
		)
	}
	interactionType := int(e.Type())
	attr = append(attr,
		attribute.Int("interaction.type", interactionType),
		attribute.String("interaction.id", e.ID().String()),
		attribute.String("interaction.application.id", e.ApplicationID().String()),
		attribute.String("interaction.user.id", e.User().ID.String()),
		attribute.String("interaction.createdat", e.CreatedAt().String()),
	)
	if e.Channel().MessageChannel != nil {
		attr = append(attr, attribute.String("interaction.channel.id", e.Channel().ID().String()))
	}
	if e.GuildID() != nil {
		attr = append(attr, attribute.String("interaction.guild.id", e.GuildID().String()))
	}

	spanName := h.spanNameFormatter(e)
	var span oteltrace.Span
	e.Ctx, span = h.tracer.Start(e.Ctx, spanName,
		oteltrace.WithSpanKind(oteltrace.SpanKindServer),
		oteltrace.WithAttributes(attr...),
	)
	defer span.End()

	activeAttrs := otelmetric.WithAttributes(
		attribute.Int("interaction.type", interactionType),
	)
	h.activeInteractions.Add(e.Ctx, 1, activeAttrs)
	defer h.activeInteractions.Add(e.Ctx, -1, activeAttrs)

	start := time.Now()
	err := h.handler(e)
	duration := time.Since(start).Seconds()

	// Route variables (e.Vars) are populated by disgo's router during h.handler(e),
	// so update the span name afterwards to replace dynamic segments with {var} placeholders.
	if len(e.Vars) > 0 {
		spanName = h.spanNameFormatter(e)
		span.SetName(spanName)
	}

	status := "ok"
	if err != nil {
		status = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	metricAttrs := otelmetric.WithAttributes(
		attribute.Int("interaction.type", interactionType),
		attribute.String("interaction.name", spanName),
		attribute.String("interaction.status", status),
	)
	h.durationHistogram.Record(e.Ctx, duration, metricAttrs)
	h.interactionCounter.Add(e.Ctx, 1, metricAttrs)

	return err
}

func defaultSpanNameFormatter(e *handler.InteractionEvent) string {
	var (
		spanPrefix string
		rawPath    string
	)
	switch i := e.Interaction.(type) {
	case discord.ApplicationCommandInteraction:
		switch d := i.Data.(type) {
		case discord.SlashCommandInteractionData:
			spanPrefix = "SlashCommand"
			rawPath = d.CommandPath()
		case discord.UserCommandInteractionData:
			spanPrefix = "UserCommand"
			rawPath = "/" + d.CommandName()
		case discord.MessageCommandInteractionData:
			spanPrefix = "MessageCommand"
			rawPath = "/" + d.CommandName()
		case discord.EntryPointCommandInteractionData:
			spanPrefix = "EntryPointCommand"
			rawPath = "/" + d.CommandName()
		}
	case discord.AutocompleteInteraction:
		spanPrefix = "Autocomplete"
		rawPath = i.Data.CommandPath()
	case discord.ComponentInteraction:
		rawPath = i.Data.CustomID()
		switch i.Data.(type) {
		case discord.ButtonInteractionData:
			spanPrefix = "Button"
		case discord.SelectMenuInteractionData:
			spanPrefix = "SelectMenu"
		}
	case discord.ModalSubmitInteraction:
		spanPrefix = "ModalSubmit"
		rawPath = i.Data.CustomID
	default:
		return "Unknown"
	}
	return fmt.Sprintf("%s: %s", spanPrefix, normalizePath(rawPath, e.Vars))
}

func normalizePath(path string, vars map[string]string) string {
	if path == "" || len(vars) == 0 {
		return path
	}
	hasLeadingSlash := strings.HasPrefix(path, "/")
	trimmed := strings.TrimPrefix(path, "/")
	if trimmed == "" {
		return path
	}
	parts := strings.Split(trimmed, "/")

	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	used := make([]bool, len(keys))
	// Walk segments from right to left so dynamic trailing segments are replaced
	// before static leading prefixes that might share the same string value.
	for i := len(parts) - 1; i >= 0; i-- {
		for idx, k := range keys {
			if !used[idx] && vars[k] == parts[i] {
				parts[i] = "{" + k + "}"
				used[idx] = true
				break
			}
		}
	}

	res := strings.Join(parts, "/")
	if hasLeadingSlash {
		return "/" + res
	}
	return res
}
