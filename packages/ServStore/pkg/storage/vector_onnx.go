//go:build cgo

package storage

import (
	"fmt"
	"math"
	"os"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var (
	onnxMutex     sync.RWMutex
	onnxLoaded    bool
	onnxSession   *ort.DynamicAdvancedSession
	onnxTokenizer *Tokenizer
)

func init() {
	// Register the ONNX implementation hooks in vector.go
	onnxInitFunc = InitializeONNXReal
	onnxEvalFunc = GenerateONNXEmbedding
}

// Tokenizer handles WordPiece tokenization
type Tokenizer struct {
	vocab map[string]int64
}

// NewTokenizer builds a vocabulary mapping
func NewTokenizer(vocabPath string) (*Tokenizer, error) {
	vocab := make(map[string]int64)
	if vocabPath != "" {
		data, err := os.ReadFile(vocabPath)
		if err == nil {
			lines := strings.Split(string(data), "\n")
			for idx, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" {
					vocab[line] = int64(idx)
				}
			}
		}
	}
	return &Tokenizer{vocab: vocab}, nil
}

// TokenizeToIDs converts text to standard BERT token input IDs
func (t *Tokenizer) TokenizeToIDs(text string) []int64 {
	clsID := int64(101)
	sepID := int64(102)
	unkID := int64(100)

	if val, ok := t.vocab["[CLS]"]; ok {
		clsID = val
	}
	if val, ok := t.vocab["[SEP]"]; ok {
		sepID = val
	}
	if val, ok := t.vocab["[UNK]"]; ok {
		unkID = val
	}

	tokens := Tokenize(text)
	var ids []int64
	ids = append(ids, clsID)

	for _, word := range tokens {
		if len(t.vocab) == 0 {
			h := fnv1a(word)
			id := int64(1000 + (h % 28000))
			ids = append(ids, id)
			continue
		}

		start := 0
		for start < len(word) {
			end := len(word)
			var curID int64
			foundSubstr := false

			for start < end {
				substr := word[start:end]
				if start > 0 {
					substr = "##" + substr
				}
				if id, ok := t.vocab[substr]; ok {
					curID = id
					foundSubstr = true
					break
				}
				end--
			}

			if !foundSubstr {
				ids = append(ids, unkID)
				break
			}

			ids = append(ids, curID)
			start = end
		}
	}

	ids = append(ids, sepID)
	return ids
}

// InitializeONNXReal initializes the ONNX runtime shared library and model
func InitializeONNXReal(sharedLibPath, modelPath, vocabPath string) error {
	onnxMutex.Lock()
	defer onnxMutex.Unlock()

	if onnxLoaded {
		return nil
	}

	ort.SetSharedLibraryPath(sharedLibPath)
	err := ort.InitializeEnvironment()
	if err != nil {
		return fmt.Errorf("failed to init ORT env: %w", err)
	}

	session, err := ort.NewDynamicAdvancedSession(modelPath,
		[]string{"input_ids", "attention_mask"},
		[]string{"last_hidden_state"},
		nil)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	tokenizer, err := NewTokenizer(vocabPath)
	if err != nil {
		session.Destroy()
		return fmt.Errorf("failed to load tokenizer: %w", err)
	}

	onnxSession = session
	onnxTokenizer = tokenizer
	onnxLoaded = true
	return nil
}

// GenerateONNXEmbedding executes forward pass and returns normalized 384-dimensional vector
func GenerateONNXEmbedding(text string) ([]float64, error) {
	onnxMutex.RLock()
	session := onnxSession
	tokenizer := onnxTokenizer
	loaded := onnxLoaded
	onnxMutex.RUnlock()

	if !loaded {
		return nil, fmt.Errorf("ONNX not initialized")
	}

	tokenIDs := tokenizer.TokenizeToIDs(text)
	if len(tokenIDs) == 0 {
		return make([]float64, 384), nil
	}

	// Create attention mask of all 1s
	attentionMask := make([]int64, len(tokenIDs))
	for i := range attentionMask {
		attentionMask[i] = 1
	}

	// Create Tensors
	inputShape := ort.Shape{1, int64(len(tokenIDs))}
	inputTensor, err := ort.NewTensor(inputShape, tokenIDs)
	if err != nil {
		return nil, err
	}
	defer inputTensor.Destroy()

	maskTensor, err := ort.NewTensor(inputShape, attentionMask)
	if err != nil {
		return nil, err
	}
	defer maskTensor.Destroy()

	outputShape := ort.Shape{1, int64(len(tokenIDs)), 384}
	outputTensor, err := ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		return nil, err
	}
	defer outputTensor.Destroy()

	err = session.Run(
		[]ort.Value{inputTensor, maskTensor},
		[]ort.Value{outputTensor},
	)
	if err != nil {
		return nil, err
	}

	// Mean Pooling
	outputData := outputTensor.GetData()
	seqLen := len(tokenIDs)
	vec := make([]float64, 384)

	for d := 0; d < 384; d++ {
		sum := 0.0
		for t := 0; t < seqLen; t++ {
			sum += float64(outputData[t*384+d])
		}
		vec[d] = sum / float64(seqLen)
	}

	// L2 Normalize
	sumSq := 0.0
	for _, val := range vec {
		sumSq += val * val
	}
	if sumSq > 0 {
		mag := math.Sqrt(sumSq)
		for i := range vec {
			vec[i] /= mag
		}
	}

	return vec, nil
}
