//go:build !wasm

package runtime

import (
	"encoding/base64"
	"encoding/hex"
)

type EncodingBase64Namespace struct{}
type EncodingHexNamespace struct{}

func Base64Encode(str interface{}) interface{} {
	sStr := toString(str)
	return base64.StdEncoding.EncodeToString([]byte(sStr))
}

func Base64Decode(str interface{}) interface{} {
	sStr := toString(str)
	decoded, err := base64.StdEncoding.DecodeString(sStr)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return string(decoded)
}

func HexEncode(str interface{}) interface{} {
	sStr := toString(str)
	return hex.EncodeToString([]byte(sStr))
}

func HexDecode(str interface{}) interface{} {
	sStr := toString(str)
	decoded, err := hex.DecodeString(sStr)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return string(decoded)
}
