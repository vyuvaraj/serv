package runtime

import (
	"os"
)

// FileRead reads the contents of a file.
func FileRead(path string) interface{} {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return string(bytes)
}

// FileWrite writes string content to a file.
func FileWrite(path string, content string) interface{} {
	err := os.WriteFile(path, []byte(content), 0644)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return true
}

// FileExists checks if a file or directory exists.
func FileExists(path string) interface{} {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	return false
}

// FileList lists files and folders inside a directory.
func FileList(path string) interface{} {
	entries, err := os.ReadDir(path)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	var list []interface{}
	for _, entry := range entries {
		list = append(list, entry.Name())
	}
	return list
}
