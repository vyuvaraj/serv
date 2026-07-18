//go:build !wasm

package runtime

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// XMLParse parses an XML string into a generic nested map structure.
func XMLParse(content string) interface{} {
	decoder := xml.NewDecoder(strings.NewReader(content))
	
	type Node struct {
		Name     string
		Children map[string]interface{}
		Content  string
	}
	
	var stack []*Node
	var root *Node
	
	for {
		t, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return [2]interface{}{nil, err.Error()}
		}
		
		switch se := t.(type) {
		case xml.StartElement:
			node := &Node{
				Name:     se.Name.Local,
				Children: make(map[string]interface{}),
			}
			for _, attr := range se.Attr {
				node.Children["@"+attr.Name.Local] = attr.Value
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				existing, ok := parent.Children[node.Name]
				if !ok {
					parent.Children[node.Name] = node.Children
				} else {
					if slice, ok := existing.([]interface{}); ok {
						parent.Children[node.Name] = append(slice, node.Children)
					} else {
						parent.Children[node.Name] = []interface{}{existing, node.Children}
					}
				}
			} else {
				root = node
			}
			stack = append(stack, node)
			
		case xml.EndElement:
			if len(stack) == 0 {
				break
			}
			node := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if len(node.Children) == 0 {
				if len(stack) > 0 {
					parent := stack[len(stack)-1]
					val := parent.Children[node.Name]
					if slice, ok := val.([]interface{}); ok {
						if len(slice) > 0 {
							slice[len(slice)-1] = node.Content
						}
					} else {
						parent.Children[node.Name] = node.Content
					}
				}
			}
			
		case xml.CharData:
			if len(stack) > 0 {
				trimmed := string(bytes.TrimSpace(se))
				if trimmed != "" {
					stack[len(stack)-1].Content += trimmed
				}
			}
		}
	}
	
	if root != nil {
		return map[string]interface{}{root.Name: root.Children}
	}
	return map[string]interface{}{}
}

// XMLStringify serializes an interface{} to an XML string.
func XMLStringify(obj interface{}) interface{} {
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	
	var serialize func(name string, val interface{}) error
	serialize = func(name string, val interface{}) error {
		switch v := val.(type) {
		case map[string]interface{}:
			start := xml.StartElement{Name: xml.Name{Local: name}}
			for k, attrVal := range v {
				if strings.HasPrefix(k, "@") {
					start.Attr = append(start.Attr, xml.Attr{
						Name:  xml.Name{Local: k[1:]},
						Value: fmt.Sprintf("%v", attrVal),
					})
				}
			}
			if err := enc.EncodeToken(start); err != nil {
				return err
			}
			for k, childVal := range v {
				if strings.HasPrefix(k, "@") {
					continue
				}
				if err := serialize(k, childVal); err != nil {
					return err
				}
			}
			if err := enc.EncodeToken(xml.EndElement{Name: xml.Name{Local: name}}); err != nil {
				return err
			}
		case []interface{}:
			for _, item := range v {
				if err := serialize(name, item); err != nil {
					return err
				}
			}
		default:
			start := xml.StartElement{Name: xml.Name{Local: name}}
			if err := enc.EncodeToken(start); err != nil {
				return err
			}
			if err := enc.EncodeToken(xml.CharData(fmt.Sprintf("%v", v))); err != nil {
				return err
			}
			if err := enc.EncodeToken(xml.EndElement{Name: xml.Name{Local: name}}); err != nil {
				return err
			}
		}
		return nil
	}
	
	if m, ok := obj.(map[string]interface{}); ok {
		for k, v := range m {
			if err := serialize(k, v); err != nil {
				return [2]interface{}{nil, err.Error()}
			}
		}
	} else {
		if err := serialize("root", obj); err != nil {
			return [2]interface{}{nil, err.Error()}
		}
	}
	
	enc.Flush()
	return buf.String()
}
