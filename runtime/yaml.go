//go:build !wasm

package runtime

import (
	"gopkg.in/yaml.v3"
)

// YAMLParse parses a YAML string into a generic structure.
func YAMLParse(content string) interface{} {
	var obj interface{}
	err := yaml.Unmarshal([]byte(content), &obj)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return obj
}

// YAMLStringify serializes an interface{} to a YAML string.
func YAMLStringify(obj interface{}) interface{} {
	data, err := yaml.Marshal(obj)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return string(data)
}
