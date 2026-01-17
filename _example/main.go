package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/disgoorg/oteldisgo"
)

const (
	Name       = "example"
	Namespace  = "github.com/disgoorg/oteldisgo/_example"
	InstanceID = "1"
	Version    = "0.0.1"
)

var (
	token        = os.Getenv("disgo_token")
	guildID      = snowflake.GetEnv("disgo_guild_id")
	otelEndpoint = os.Getenv("otel_endpoint")
	otelSecure   = os.Getenv("otel_secure")
	commands     = []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{
			Name:        "ping",
			Description: "Replies with pong",
		},
	}
)

func main() {
	slog.SetLogLoggerLevel(slog.LevelDebug)

	tracer, err := newTracer()
	if err != nil {
		slog.Error("error while getting tracer", slog.Any("err", err))
		return
	}

	r := handler.New()
	r.Use(oteldisgo.Middleware("example"))
	r.Command("/ping", pingHandler(tracer))

	client, err := disgo.New(token,
		bot.WithDefaultGateway(),
		bot.WithRestClientConfigOpts(
			rest.WithHTTPClient(&http.Client{
				Transport: otelhttp.NewTransport(nil),
				Timeout:   5 * time.Second,
			}),
		),
		bot.WithEventListeners(r),
	)
	if err != nil {
		slog.Error("error while building disgo", slog.Any("err", err))
		return
	}

	if err = handler.SyncCommands(client, commands, []snowflake.ID{guildID}); err != nil {
		slog.Error("error while syncing commands", slog.Any("err", err))
	}

	defer client.Close(context.TODO())

	if err = client.OpenGateway(context.TODO()); err != nil {
		slog.Error("error while opening gateway", slog.Any("err", err))
		return
	}

	slog.Info("example is now running. Press CTRL-C to exit.")
	s := make(chan os.Signal, 1)
	signal.Notify(s, syscall.SIGINT, syscall.SIGTERM)
	<-s
}

func pingHandler(tracer trace.Tracer) func(event *handler.CommandEvent) error {
	return func(event *handler.CommandEvent) error {
		ctx, span := tracer.Start(event.Ctx, "ping",
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attribute.String("my.attribute", "test")),
		)
		defer span.End()

		return event.CreateMessage(discord.MessageCreate{
			Content: "pong",
		}, rest.WithCtx(ctx))
	}
}
