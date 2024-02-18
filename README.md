[![Go Reference](https://pkg.go.dev/badge/github.com/disgoorg/oteldisgo.svg)](https://pkg.go.dev/github.com/disgoorg/oteldisgo)
[![Go Report](https://goreportcard.com/badge/github.com/disgoorg/oteldisgo)](https://goreportcard.com/report/github.com/disgoorg/oteldisgo)
[![Go Version](https://img.shields.io/github/go-mod/go-version/disgoorg/oteldisgo)](https://golang.org/doc/devel/release.html)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://github.com/disgoorg/oteldisgo/blob/master/LICENSE)
[![Otel DisGo Version](https://img.shields.io/github/v/tag/disgoorg/oteldisgo?label=release)](https://github.com/disgoorg/oteldisgo/releases/latest)
[![DisGo Discord](https://discord.com/api/guilds/817327181659111454/widget.png)](https://discord.gg/TewhTfDpvW)

# OtelDisGo

OtelDisGo is a [DisGo](https://github.com/disgoorg/disgo) handler middleware for [OpenTelemetry](https://opentelemetry.io/). It provides a simple way to trace your DisGo commands.

## Summary

1. [Getting Started](#getting-started)
2. [Documentation](#documentation)
3. [Troubleshooting](#troubleshooting)
4. [Contributing](#contributing)
5. [License](#license)

## Getting Started

### Installing

```sh
$ go get github.com/disgoorg/oteldisgo
```

### Usage

Check https://opentelemetry.io/docs/languages/go/getting-started/ for more information on how to set up OpenTelemetry.

```go
package main

import (
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/oteldisgo"
)

func main() {
	r := handler.New()
	r.Use(oteldisgo.Middleware("example"))
	// handle commands as usual here
}

```

### OTEL Tracing

OtelDisGo provides the following spans attributes:

1. For All:
   - `interaction.tye` - The type of the interaction
   - `interaction.id` - The id of the interaction
   - `interaction.application.id` - The id of the application
   - `interaction.user.id` - The id of the user
   - `interaction.channel.id` - The id of the channel
   - `interaction.guild.id` - The id of the guild (if applicable)
   - `interaction.createdat` - The time the interaction was created
2. Application Command Interaction
    - `interaction.command.id` - The id of the command
    - `interaction.command.name` - The name of the command
    - `interaction.command.guild.id` - The guild id of the command (if applicable)
   1. Slash Command Interaction
      - `interaction.command.subcommand` - The subcommand of the command
      - `interaction.command.subcommandgroup` - The subcommand group of the command
      - `interaction.command.path` - The full path of the command
   2. User Command Interaction
      - `interaction.command.user.id` - The id of the user who the command was used on
   3. Message Command Interaction
      - `interaction.command.message.id` - The id of the message the command was used on
3. AutoComplete Interaction
    - `interaction.command.id` - The id of the command
    - `interaction.command.name` - The name of the command
    - `interaction.command.subcommand` - The subcommand of the command
    - `interaction.command.subcommandgroup` - The subcommand group of the command
    - `interaction.command.path` - The full path of the command
    - `interaction.command.guild.id` - The guild id of the command (if applicable)
4. Component Interaction
    - `interaction.component.type` - The type of the component
    - `interaction.component.customid` - The custom id of the component
5. Modal Interaction
    - `interaction.component.customid` - The custom id of the modal

## Documentation

Documentation can be found under

* [![Go Reference](https://pkg.go.dev/badge/github.com/disgoorg/oteldisgo.svg)](https://pkg.go.dev/github.com/disgoorg/oteldisgo)
* [![OpenTelemetry Documentation](https://img.shields.io/badge/OpenTelemetry%20Documentation-blue.svg)](https://opentelemetry.io/docs/languages/go/)

## Troubleshooting

For help feel free to open an issue or reach out on [Discord](https://discord.gg/TewhTfDpvW)

## Contributing

Contributions are welcomed but for bigger changes we recommend first reaching out via [Discord](https://discord.gg/TewhTfDpvW) or create an issue to discuss your problems, intentions and ideas.

## License

Distributed under the [![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE). See LICENSE for more information.

## Supported by Jetbrains

<a href="https://www.jetbrains.com/community/opensource" target="_blank" title="Jetbrain Open Source Community Support"><img src="https://resources.jetbrains.com/storage/products/company/brand/logos/jb_beam.png" alt="Jetbrain Open Source Community Support" width="400px">
