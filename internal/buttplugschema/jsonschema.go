package buttplugschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/qri-io/jsonschema"
)

type properties struct {
	jsonschema.Properties
	keys []string
}

func newProperties() jsonschema.Keyword {
	return &properties{}
}

func (p *properties) UnmarshalJSON(b []byte) error {
	if err := json.Unmarshal(b, &p.Properties); err != nil {
		return err
	}

	// https://gitlab.com/c0b/go-ordered-json/blob/febf46534d5a/ordered.go#L149
	dec := json.NewDecoder(bytes.NewReader(b))

	if err := jsonAssertNextToken(dec, json.Delim('{')); err != nil {
		return err
	}

	var garbage map[string]interface{}

	p.keys = make([]string, 0, len(p.Properties))
	for i := 0; i < len(p.Properties); i++ {
		if !dec.More() {
			return io.ErrUnexpectedEOF
		}

		t, err := dec.Token()
		if err != nil {
			return err
		}

		key, ok := t.(string)
		if !ok {
			return fmt.Errorf("object key not string but %v", t)
		}

		p.keys = append(p.keys, key)

		// Consume everything.
		if err = dec.Decode(&garbage); err != nil {
			return err
		}
	}

	if err := jsonAssertNextToken(dec, json.Delim('}')); err != nil {
		return err
	}

	return nil
}

func (p *properties) Each(f func(k string, v *jsonschema.Schema)) {
	if p == nil {
		return
	}
	for _, key := range p.keys {
		f(key, p.Properties[key])
	}
}

func jsonAssertNextToken(dec *json.Decoder, v json.Token) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}

	if t != v {
		return fmt.Errorf("expected token %v, got %v", v, t)
	}

	return nil
}
