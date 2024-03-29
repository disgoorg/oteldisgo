package oteldisgo

import (
	"fmt"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"go.opentelemetry.io/otel/attribute"
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
			serverName: serverName,
			handler:    handler,
			tracer:     tracer,
			meter:      meter,
		}
		return h.Handle
	}
}

type otelHandler struct {
	serverName string
	handler    handler.Handler
	tracer     oteltrace.Tracer
	meter      otelmetric.Meter
}

func (h *otelHandler) Handle(e *handler.InteractionEvent) error {
	var (
		spanName string
		attr     []attribute.KeyValue
	)
	switch i := e.Interaction.(type) {
	case discord.ApplicationCommandInteraction:
		switch d := i.Data.(type) {
		case discord.SlashCommandInteractionData:
			spanName = fmt.Sprintf("SlashCommand: %s", d.CommandPath())
			if d.SubCommandName != nil {
				attr = append(attr, attribute.String("interaction.command.subcommand", *d.SubCommandName))
			}
			if d.SubCommandGroupName != nil {
				attr = append(attr, attribute.String("interaction.command.subcommandgroup", *d.SubCommandGroupName))
			}
			attr = append(attr, attribute.String("interaction.command.path", d.CommandPath()))
		case discord.UserCommandInteractionData:
			spanName = fmt.Sprintf("UserCommand: /%s", d.CommandName())
			attr = append(attr, attribute.String("interaction.command.user.id", d.TargetID().String()))
		case discord.MessageCommandInteractionData:
			spanName = fmt.Sprintf("MessageCommand: /%s", d.CommandName())
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
		spanName = fmt.Sprintf("Autocomplete: %s", i.Data.CommandPath())
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
		switch i.Data.(type) {
		case discord.ButtonInteractionData:
			spanName = fmt.Sprintf("Button: %s", i.Data.CustomID())
		case discord.SelectMenuInteractionData:
			spanName = fmt.Sprintf("SelectMenu: %s", i.Data.CustomID())
		}
		attr = append(attr,
			attribute.Int("interaction.component.type", int(i.Data.Type())),
			attribute.String("interaction.component.customid", i.Data.CustomID()),
		)

	case discord.ModalSubmitInteraction:
		spanName = fmt.Sprintf("ModalSubmit: %s", i.Data.CustomID)
		attr = append(attr,
			attribute.String("interaction.modal.customid", i.Data.CustomID),
		)
	default:
		spanName = "Unknown"
	}
	attr = append(attr,
		attribute.Int("interaction.type", int(e.Interaction.Type())),
		attribute.String("interaction.id", e.Interaction.ID().String()),
		attribute.String("interaction.application.id", e.Interaction.ApplicationID().String()),
		attribute.String("interaction.user.id", e.Interaction.User().ID.String()),
		attribute.String("interaction.channel.id", e.Interaction.Channel().ID().String()),
		attribute.String("interaction.createdat", e.Interaction.CreatedAt().String()),
	)
	if e.Interaction.GuildID() != nil {
		attr = append(attr, attribute.String("interaction.guild.id", e.Interaction.GuildID().String()))
	}

	var span oteltrace.Span
	e.Ctx, span = h.tracer.Start(e.Ctx, spanName,
		oteltrace.WithSpanKind(oteltrace.SpanKindServer),
		oteltrace.WithAttributes(attr...),
	)

	defer span.End()
	return h.handler(e)
}
