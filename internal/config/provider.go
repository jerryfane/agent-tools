package config

import (
	"bytes"
	"os"

	"github.com/BurntSushi/toml"
)

type mapProvider struct {
	values map[string]interface{}
}

func (p mapProvider) ReadBytes() ([]byte, error) {
	return nil, nil
}

func (p mapProvider) Read() (map[string]interface{}, error) {
	return p.values, nil
}

type fileProvider string

func (p fileProvider) ReadBytes() ([]byte, error) {
	return os.ReadFile(string(p))
}

func (p fileProvider) Read() (map[string]interface{}, error) {
	return nil, nil
}

type tomlParser struct{}

func (tomlParser) Unmarshal(data []byte) (map[string]interface{}, error) {
	out := map[string]interface{}{}
	if _, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (tomlParser) Marshal(value map[string]interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
