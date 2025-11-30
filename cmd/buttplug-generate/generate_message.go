package main

import (
	"log/slog"

	j "github.com/dave/jennifer/jen"
	"libdb.so/go-buttplug/cmd/buttplug-generate/jsonschema"
	"github.com/diamondburned/gotk4/gir/girgen/strcases"
)

func (gen *generator) generateMessageSpec(schema *jsonschema.Schema) {
	properties, ok := schema.Properties()
	if !ok {
		slog.Error(
			"message spec schema has no properties",
			"schema.location", schema.Location())
		return
	}

	// Append "Message" to all message types.
	for i, property := range properties {
		property.Schema = property.Schema.
			Unref().
			WithName(property.Schema.Name() + "Message")
		properties[i] = property
	}

	/*
	 * MessageType
	 */

	gen.file.Comment("MessageType is the type string used to identify a buttplug.io message from the wire.")
	gen.file.Type().Id("MessageType").String().Line()
	gen.file.Line()

	gen.file.Comment("All defined message types.")
	gen.file.Const().DefsFunc(func(g *j.Group) {
		for _, property := range properties {
			g.
				Commentf("MessageType%s is the type string for a [%s].", property.Name, property.Schema.Name()).
				Line().
				Id("MessageType" + property.Name).Id("MessageType").Op("=").Lit(property.JSONName)
		}
	})
	gen.file.Line()

	/*
	 * MessagePayload type and parser
	 */

	gen.file.
		Comment("Payload is a list of messages sent from the buttplug.io websocket server.").Line().
		Comment("Use this type to parse each message in the payload.").Line().
		Type().Id("Payload").Index().Id("Message").Line()

	gen.file.Var().Defs(
		j.Id("_").Qual("encoding/json/v2", "UnmarshalerFrom").Op("=").Parens(j.Op("*").Id("Payload")).Call(j.Nil()),
		j.Id("_").Qual("encoding/json/v2", "MarshalerTo").Op("=").Id("Payload").Call(j.Nil()),
		j.Id("_").Qual("log/slog", "LogValuer").Op("=").Id("Payload").Call(j.Nil()),
	)

	gen.file.
		Comment("UnmarshalJSONFrom implements [json.UnmarshalerFrom].").Line().
		Comment("It consumes one full array of messages from the given JSON decoder.").Line().
		Func().
		Params(j.Id("p").Op("*").Id("Payload")).
		Id("UnmarshalJSONFrom").
		Params(j.Id("decoder").Op("*").Qual("encoding/json/jsontext", "Decoder")).
		Params(j.Error()).
		Block(
			j.Id("readTokenKind").Op(":=").
				Func().
				Params(j.Id("need").Qual("encoding/json/jsontext", "Kind")).
				Params(j.Error()).
				Block(
					j.List(j.Id("t"), j.Id("err")).Op(":=").Id("decoder").Dot("ReadToken").Call(),
					j.If(j.Id("err").Op("!=").Nil()).Block(
						j.Return(j.Qual("fmt", "Errorf").Call(
							j.Lit("failed to read token for %v: %w"),
							j.Id("need"), j.Id("err"),
						)),
					),
					j.If(j.Id("t").Dot("Kind").Call().Op("!=").Id("need")).Block(
						j.Return(j.Qual("fmt", "Errorf").Call(
							j.Lit("expected %v token, got %v"),
							j.Id("need"), j.Id("t"),
						)),
					),
					j.Return(j.Nil()),
				),
			j.Line(),

			j.For().BlockFunc(func(g *j.Group) {
				g.Comment("Advance reader into the array body.")
				g.If(
					j.Id("err").Op(":=").Id("readTokenKind").Call(j.Qual("encoding/json/jsontext", "Kind").Call(j.LitRune('['))),
					j.Id("err").Op("!=").Nil(),
				).Block(
					j.Return(j.Qual("fmt", "Errorf").Call(
						j.Lit("failed to read start of array token: %w"),
						j.Id("err"),
					)),
				)
				g.Line()

				g.For().Block(
					j.Comment("Advance reader into message body."),
					j.List(j.Id("t"), j.Id("err")).Op(":=").Id("decoder").Dot("ReadToken").Call(),
					j.If(j.Id("err").Op("!=").Nil()).Block(
						j.Return(
							j.Qual("fmt", "Errorf").Call(
								j.Lit("failed to read token: %w"),
								j.Id("err"),
							),
						),
					),
					j.Line(),

					j.Switch(j.Id("t").Dot("Kind").Call()).Block(
						j.Case(j.LitRune(']')).Block(
							j.Comment("End of array, we're done."),
							j.Return(j.Nil()),
						),
						j.Case(j.LitRune('{')).Block(
							j.Comment("Start of object, continue processing."),
						),
						j.Default().Block(
							j.Return(
								j.Qual("fmt", "Errorf").Call(
									j.Lit("expected start of object or end of array, got %v"),
									j.Id("t").Dot("Kind").Call(),
								),
							),
						),
					),
					j.Line(),

					j.Var().Id("key").String(),
					j.If(
						j.Id("err").Op(":=").Qual("encoding/json/v2", "UnmarshalDecode").Call(
							j.Id("decoder"),
							j.Op("&").Id("key"),
						),
						j.Id("err").Op("!=").Nil(),
					).Block(
						j.Return(
							j.Qual("fmt", "Errorf").Call(
								j.Lit("failed to unmarshal message type key: %w"),
								j.Id("err"),
							),
						),
					),
					j.Line(),

					j.Switch(j.Id("MessageType").Call(j.Id("key"))).BlockFunc(func(g *j.Group) {
						for _, property := range properties {
							typeName := property.Schema.Name()
							typeKey := "MessageType" + property.Name

							g.Case(j.Id(typeKey))

							g.Var().Id("msg").Id(typeName)
							g.If(
								j.Err().Op(":=").Qual("encoding/json/v2", "UnmarshalDecode").Call(
									j.Id("decoder"),
									j.Op("&").Id("msg"),
								),
								j.Err().Op("!=").Nil(),
							).Block(
								j.Return(
									j.Qual("fmt", "Errorf").Call(
										j.Lit("failed to unmarshal message of type %q: %w"),
										j.Id("key"),
										j.Id("err"),
									),
								),
							)
							g.Op("*").Id("p").Op("=").Append(j.Op("*").Id("p"), j.Op("&").Id("msg"))
							g.Line()
						}

						g.Default()
						g.List(j.Id("v"), j.Id("err")).Op(":=").Id("decoder").Dot("ReadValue").Call()
						g.If(j.Id("err").Op("!=").Nil()).Block(
							j.Return(
								j.Qual("fmt", "Errorf").Call(
									j.Lit("failed to read unknown buttplug.io message type %q: %w"),
									j.Id("key"), j.Id("err"),
								),
							),
						)
						g.Qual("log/slog", "Warn").Call(
							j.Line().Lit("received unknown buttplug.io message type, ignoring"),
							j.Line().Qual("log/slog", "String").Call(j.Lit("msg.type"), j.Id("key")),
							j.Line().Qual("log/slog", "Any").Call(j.Lit("msg.body"), j.Id("v")),
						)
					}),
					j.Line(),

					j.Comment("Advance reader out of the object body."),
					j.If(
						j.Id("err").Op("=").Id("readTokenKind").Call(j.Qual("encoding/json/jsontext", "Kind").Call(j.LitRune('}'))),
						j.Id("err").Op("!=").Nil(),
					).Block(
						j.Return(
							j.Qual("fmt", "Errorf").Call(
								j.Lit("failed to read end of object token: %w"),
								j.Id("err"),
							),
						),
					),
				)
			}),
		)

	// TODO: optimize this with encoding
	gen.file.
		Comment("MarshalJSONTo writes the payload as the JSON websocket wire protocol.").Line().
		Func().
		Params(j.Id("p").Id("Payload")).
		Id("MarshalJSONTo").
		Params(j.Id("encoder").Op("*").Qual("encoding/json/jsontext", "Encoder")).
		Params(j.Error()).
		BlockFunc(func(g *j.Group) {
			genWriteToken := func(token j.Code, what string) j.Code {
				return j.If(
					j.Id("err").Op(":=").Id("encoder").Dot("WriteToken").Call(token),
					j.Id("err").Op("!=").Nil(),
				).Block(
					j.Return(
						j.Qual("fmt", "Errorf").Call(
							j.Lit("failed to write "+what+" token: %w"),
							j.Id("err"),
						),
					),
				)
			}

			g.Add(genWriteToken(
				j.Qual("encoding/json/jsontext", "BeginArray"),
				"start of payload array",
			))
			g.Line()

			g.For(
				j.List(j.Id("i"), j.Id("msg")).Op(":=").Range().Id("p"),
			).Block(
				genWriteToken(
					j.Qual("encoding/json/jsontext", "BeginObject"),
					"start of message object",
				),
				j.Line(),

				genWriteToken(
					j.Qual("encoding/json/jsontext", "String").Call(
						j.String().Call(j.Id("msg").Dot("Type").Call()),
					),
					"message type key",
				),
				j.Line(),

				j.If(
					j.Id("err").Op(":=").Qual("encoding/json/v2", "MarshalEncode").Call(
						j.Id("encoder"),
						j.Id("msg"),
					),
					j.Id("err").Op("!=").Nil(),
				).Block(
					j.Return(
						j.Qual("fmt", "Errorf").Call(
							j.Lit("failed to marshal message[%d] (type %T): %w"),
							j.Id("i"), j.Id("msg"), j.Id("err"),
						),
					),
				),
				j.Line(),

				genWriteToken(
					j.Qual("encoding/json/jsontext", "EndObject"),
					"end of message object",
				),
			)
			g.Line()

			g.If(
				j.Id("err").Op(":=").Id("encoder").Dot("WriteToken").Call(
					j.Qual("encoding/json/jsontext", "EndArray"),
				),
				j.Id("err").Op("!=").Nil(),
			).Block(
				j.Return(
					j.Qual("fmt", "Errorf").Call(
						j.Lit("failed to write end of array token: %w"),
						j.Id("err"),
					),
				),
			)
			g.Line()

			g.Return(j.Nil())
		})
	gen.file.Line()

	gen.file.
		Comment("LogValue implements [slog.LogValuer].").Line().
		Func().
		Params(j.Id("p").Id("Payload")).
		Id("LogValue").
		Params().
		Params(j.Qual("log/slog", "Value")).
		Block(
			j.Id("attrs").Op(":=").Make(j.Index().Qual("log/slog", "Attr"), j.Len(j.Id("p"))),
			j.For(j.List(j.Id("i"), j.Id("msg")).Op(":=").Range().Id("p")).Block(
				j.Id("attrs").Index(j.Id("i")).Op("=").Qual("log/slog", "Any").Call(
					j.Qual("fmt", "Sprintf").Call(j.Lit("payload[%d]"), j.Id("i")),
					j.Id("msg"),
				),
			),
			j.Return(
				j.Qual("log/slog", "GroupValue").Call(j.Id("attrs").Op("...")),
			),
		)
	gen.file.Line()

	/*
	 * Message interface
	 */

	gen.file.Comment("Message represents a single message in the buttplug.io protocol.")
	gen.file.Comment("")
	gen.file.Comment("The following messages are defined:")
	for _, property := range properties {
		gen.file.Commentf("//\t- [%s]", property.Schema.Name())
	}
	gen.file.Type().Id("Message").InterfaceFunc(func(g *j.Group) {
		g.Comment("Type returns the message type of the message.")
		g.Id("Type").Params().Id("MessageType")
		g.Line()
		g.Id("isMessage").Call()
	})
	gen.file.Line()

	/*
	 * ClientMesage interface
	 */

	gen.file.Comment("ClientMessage represents a message that can be sent from the client to the server.")
	gen.file.Comment("It extends [Message] with a way to set the client ID to be used when sending the message.")
	gen.file.Comment("")
	gen.file.Comment("The following messages are defined:")
	for _, property := range properties {
		if deriveMessageType(property.Schema) == clientMessage {
			gen.file.Commentf("//\t- [%s]", property.Schema.Name())
		}
	}
	gen.file.Type().Id("ClientMessage").InterfaceFunc(func(g *j.Group) {
		g.Id("Message")
		g.Comment("ClientID returns the ID field of the message.")
		g.Id("ClientID").Params().Id("ClientID")
		g.Comment("WithID sets the ID field of the shallow-copied message and returns the modified message.")
		g.Id("WithID").Params(j.Id("id").Id("ClientID")).Id("ClientMessage")
		g.Line()
		g.Id("isClientMessage").Call()
	})

	/*
	 * ServerMessage interface
	 */

	gen.file.Comment("ServerMessage represents a message that can be sent from the server to the client.")
	gen.file.Comment("It extends [Message].")
	gen.file.Comment("")
	gen.file.Comment("The following messages are defined:")
	for _, property := range properties {
		if deriveMessageType(property.Schema) == serverMessage {
			gen.file.Commentf("//\t- [%s]", property.Schema.Name())
		}
	}
	gen.file.Type().Id("ServerMessage").InterfaceFunc(func(g *j.Group) {
		g.Id("Message")
		g.Comment("ServerID returns the ID field of the message.")
		g.Id("ServerID").Params().Id("ServerID")
		g.Line()
		g.Id("isServerMessage").Call()
	})

	/*
	 * InternalMessage interface
	 */

	gen.file.Comment("InternalMessage is a special type that the library uses for internal-only messages.")
	gen.file.Comment("Messages of this type were never emitted from the server.")
	gen.file.Type().Id("InternalMessage").Struct()
	gen.file.Line()

	gen.file.Add(generateImplementorMethod("InternalMessage", "isMessage"))
	gen.file.Add(generateImplementorMethod("InternalMessage", "isInternalMessage"))
	gen.file.Line()

	gen.file.
		Comment("Type returns an empty string for internal messages.").
		Line().
		Func().
		Params(j.Id("m").Id("InternalMessage")).
		Id("Type").
		Params().
		Params(j.Id("MessageType")).
		Block(
			j.Return(j.Lit("")),
		)

	gen.file.
		Comment("IsInternalMessage returns whether the message is an internal-only message.").
		Line().
		Func().
		Id("IsInternalMessage").
		Params(j.Id("m").Id("Message")).
		Params(j.Bool()).
		BlockFunc(func(g *j.Group) {
			g.List(j.Id("_"), j.Id("ok")).Op(":=").Id("m").Assert(
				j.Interface(j.Id("isInternalMessage").Call()),
			)
			g.Return(j.Id("ok"))
		})
	gen.file.Line()

	gen.file.
		Comment("MarshalJSON returns an error because internal messages cannot be marshaled.").
		Line().
		Func().
		Params(j.Id("m").Id("InternalMessage")).
		Id("MarshalJSON").
		Params().
		Params(j.Index().Byte(), j.Error()).
		BlockFunc(func(g *j.Group) {
			g.Return(j.Nil(), j.Qual("errors", "New").Call(j.Lit("internal messages cannot be marshaled")))
		})
	gen.file.Line()

	/*
	 * Message generation
	 */

	for _, property := range properties {
		msgType := deriveMessageType(property.Schema)
		keyType := "MessageType" + property.Name
		recv := j.Id(strcases.FirstLetter(property.Schema.Name()))

		gen.file.
			Commentf("Type returns [%s].", keyType).Line().
			Func().
			Params(j.Add(recv).Op("*").Id(property.Schema.Name())).
			Id("Type").
			Params().
			Params(j.Id("MessageType")).
			Block(
				j.Return(j.Id(keyType)),
			)
		gen.file.Line()

		gen.file.Add(generateImplementorMethod(property.Schema.Name(), "isMessage"))

		switch msgType {
		case clientMessage:
			gen.file.Add(generateImplementorMethod(property.Schema.Name(), "isClientMessage"))
			gen.file.Line()

			gen.file.
				Comment("ClientID implements [ClientMessage]").
				Line().
				Func().
				Params(j.Add(recv).Op("*").Id(property.Schema.Name())).
				Id("ClientID").
				Params().
				Params(j.Id("ClientID")).
				Block(
					j.Return(j.Add(recv).Dot("ID")),
				)
			gen.file.Line()

			gen.file.
				Comment("WithID implements [ClientMessage].").
				Line().
				Func().
				Params(j.Add(recv).Op("*").Id(property.Schema.Name())).
				Id("WithID").
				Params(j.Id("id").Id("ClientID")).
				Params(j.Id("ClientMessage")).
				BlockFunc(func(g *j.Group) {
					g.Add(recv).Dot("ID").Op("=").Id("id")
					g.Return(j.Add(recv))
				})
			gen.file.Line()

		case serverMessage:
			gen.file.Add(generateImplementorMethod(property.Schema.Name(), "isServerMessage"))
			gen.file.Line()

			gen.file.
				Comment("ServerID implements [ServerMessage]").
				Line().
				Func().
				Params(j.Add(recv).Op("*").Id(property.Schema.Name())).
				Id("ServerID").
				Params().
				Params(j.Id("ServerID")).
				Block(
					j.Return(j.Add(recv).Dot("ID")),
				)
			gen.file.Line()
		}

		gen.file.
			Comment("LogValue implements [slog.LogValuer].").Line().
			Func().
			Params(j.Add(recv).Op("*").Id(property.Schema.Name())).
			Id("LogValue").
			Params().
			Params(j.Qual("log/slog", "Value")).
			Block(
				j.Type().Id("raw").Id(property.Schema.Name()),
				j.Return(
					j.Qual("log/slog", "GroupValue").CallFunc(func(g *j.Group) {
						g.Line().Qual("log/slog", "String").Call(j.Lit("type"), j.Lit(property.JSONName))
						g.Line().Qual("log/slog", "Any").Call(
							j.Lit("data"),
							j.Parens(j.Op("*").Id("raw")).Call(recv),
						)
					}),
				),
			)
		gen.file.Line()
	}

	for _, property := range properties {
		gen.enqueue(property.Schema.Unref())
	}
}

func generateImplementorMethod(typeName, methodName string) j.Code {
	return j.
		Func().
		Params(j.Id(strcases.FirstLetter(typeName)).Op("*").Id(typeName)).
		Id(methodName).
		Params().
		Block()
}

type messageType int

const (
	_ messageType = iota
	clientMessage
	serverMessage
)

// deriveMessageType derives the message type from the given message schema's
// properties.
func deriveMessageType(schema *jsonschema.Schema) messageType {
	if anyOfs := schema.AnyOf(); len(anyOfs) == 1 && schema.Type().Is(jsonschema.ObjectType) {
		// unwrap object of single anyOf (treat it as alias).
		schema = anyOfs[0].Unref()
	}

	properties, _ := schema.Properties()
	for _, prop := range properties {
		if prop.Name != "ID" || !prop.Schema.IsRef() || !prop.IsRequired() {
			continue
		}
		switch prop.Schema.Unref().Location().BaseName() {
		case "ClientId":
			return clientMessage
		case "ServerId":
			return serverMessage
		}
	}

	return 0
}
