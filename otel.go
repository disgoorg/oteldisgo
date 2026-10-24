package oteldisgo

import (
	"fmt"
	"slices"
	"strings"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelmetric "go.opentelemetry.io/otel/metric"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func Middleware(serverName string, opts ...ConfigOpt) handler.Middleware {
	cfg := DefaultConfig()
	cfg.Apply(opts)

	tracer := cfg.TracerProvider.Tracer(
		disgo.Module,
		oteltrace.WithInstrumentationVersion(disgo.SemVersion),
	)
	meter := cfg.MeterProvider.Meter(
		disgo.Module,
		otelmetric.WithInstrumentationVersion(disgo.SemVersion),
	)

	return func(handler handler.Handler) handler.Handler {
		h := &otelHandler{
			serverName:        serverName,
			handler:           handler,
			tracer:            tracer,
			meter:             meter,
			filter:            cfg.Filter,
			spanNameFormatter: cfg.SpanNameFormatter,
		}
		return h.Handle
	}
}

type otelHandler struct {
	serverName        string
	handler           handler.Handler
	tracer            oteltrace.Tracer
	meter             otelmetric.Meter
	filter            FilterFunc
	spanNameFormatter SpanNameFormatter
}

func (h *otelHandler) Handle(e *handler.InteractionEvent) error {
	if h.filter != nil && !h.filter(e) {
		return h.handler(e)
	}

	var (
		spanPrefix string
		rawPath    string
		spanName   string
		attr       []attribute.KeyValue
	)
	switch i := e.Interaction.(type) {
	case discord.ApplicationCommandInteraction:
		switch d := i.Data.(type) {
		case discord.SlashCommandInteractionData:
			spanPrefix = "SlashCommand"
			rawPath = d.CommandPath()
			spanName = fmt.Sprintf("%s: %s", spanPrefix, rawPath)
			if d.SubCommandName != nil {
				attr = append(attr, attribute.String("interaction.command.subcommand", *d.SubCommandName))
			}
			if d.SubCommandGroupName != nil {
				attr = append(attr, attribute.String("interaction.command.subcommandgroup", *d.SubCommandGroupName))
			}
			attr = append(attr, attribute.String("interaction.command.path", rawPath))
		case discord.UserCommandInteractionData:
			spanPrefix = "UserCommand"
			rawPath = "/" + d.CommandName()
			spanName = fmt.Sprintf("%s: %s", spanPrefix, rawPath)
			attr = append(attr, attribute.String("interaction.command.user.id", d.TargetID().String()))
		case discord.MessageCommandInteractionData:
			spanPrefix = "MessageCommand"
			rawPath = "/" + d.CommandName()
			spanName = fmt.Sprintf("%s: %s", spanPrefix, rawPath)
			attr = append(attr, attribute.String("interaction.command.message.id", d.TargetID().String()))
		case discord.EntryPointCommandInteractionData:
			spanPrefix = "EntryPointCommand"
			rawPath = "/" + d.CommandName()
			spanName = fmt.Sprintf("%s: %s", spanPrefix, rawPath)
		}
		attr = append(attr,
			attribute.String("interaction.command.name", i.Data.CommandName()),
			attribute.String("interaction.command.id", i.Data.CommandID().String()),
		)
		if i.Data.GuildID() != nil {
			attr = append(attr, attribute.String("interaction.command.guild.id", i.Data.GuildID().String()))
		}
	case discord.AutocompleteInteraction:
		spanPrefix = "Autocomplete"
		rawPath = i.Data.CommandPath()
		spanName = fmt.Sprintf("%s: %s", spanPrefix, rawPath)
		if i.Data.SubCommandName != nil {
			attr = append(attr, attribute.String("interaction.command.subcommand", *i.Data.SubCommandName))
		}
		if i.Data.SubCommandGroupName != nil {
			attr = append(attr, attribute.String("interaction.command.subcommandgroup", *i.Data.SubCommandGroupName))
		}
		attr = append(attr,
			attribute.String("interaction.command.path", rawPath),
			attribute.String("interaction.command.name", i.Data.CommandName),
			attribute.String("interaction.command.id", i.Data.CommandID.String()),
		)
		if i.Data.GuildID != nil {
			attr = append(attr, attribute.String("interaction.command.guild.id", i.Data.GuildID.String()))
		}
	case discord.ComponentInteraction:
		rawPath = i.Data.CustomID()
		switch i.Data.(type) {
		case discord.ButtonInteractionData:
			spanPrefix = "Button"
			spanName = fmt.Sprintf("%s: %s", spanPrefix, rawPath)
		case discord.SelectMenuInteractionData:
			spanPrefix = "SelectMenu"
			spanName = fmt.Sprintf("%s: %s", spanPrefix, rawPath)
		}
		attr = append(attr,
			attribute.Int("interaction.component.type", int(i.Data.Type())),
			attribute.String("interaction.component.customid", rawPath),
		)
	case discord.ModalSubmitInteraction:
		spanPrefix = "ModalSubmit"
		rawPath = i.Data.CustomID
		spanName = fmt.Sprintf("%s: %s", spanPrefix, rawPath)
		attr = append(attr,
			attribute.String("interaction.modal.customid", rawPath),
		)
	default:
		spanName = "Unknown"
	}
	attr = append(attr,
		attribute.Int("interaction.type", int(e.Interaction.Type())),
		attribute.String("interaction.id", e.Interaction.ID().String()),
		attribute.String("interaction.application.id", e.Interaction.ApplicationID().String()),
		attribute.String("interaction.user.id", e.Interaction.User().ID.String()),
		attribute.String("interaction.createdat", e.Interaction.CreatedAt().String()),
	)
	if e.Interaction.Channel().MessageChannel != nil {
		attr = append(attr, attribute.String("interaction.channel.id", e.Interaction.Channel().ID().String()))
	}
	if e.Interaction.GuildID() != nil {
		attr = append(attr, attribute.String("interaction.guild.id", e.Interaction.GuildID().String()))
	}

	if h.spanNameFormatter != nil {
		spanName = h.spanNameFormatter(e)
	}

	var span oteltrace.Span
	e.Ctx, span = h.tracer.Start(e.Ctx, spanName,
		oteltrace.WithSpanKind(oteltrace.SpanKindServer),
		oteltrace.WithAttributes(attr...),
	)
	defer span.End()

	err := h.handler(e)
	// Route variables (e.Vars) are populated by disgo's router during h.handler(e),
	// so update the span name afterwards to replace dynamic segments with {var} placeholders.
	if h.spanNameFormatter != nil {
		span.SetName(h.spanNameFormatter(e))
	} else if len(e.Vars) > 0 && rawPath != "" {
		span.SetName(fmt.Sprintf("%s: %s", spanPrefix, normalizePath(rawPath, e.Vars)))
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
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
