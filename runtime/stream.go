package runtime

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

type Stream struct {
	mu       sync.Mutex
	topic    string
	channels []chan interface{}
	done     chan struct{}
}

func NewStream(topic string) *Stream {
	s := &Stream{
		topic: topic,
		done:  make(chan struct{}),
	}
	
	ch := make(chan interface{}, 100)
	s.channels = append(s.channels, ch)
	
	Subscribe(topic, func(payload string) {
		var val interface{} = payload
		var decoded map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &decoded); err == nil {
			val = NewSafeMapFromMap(decoded)
		}
		
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, c := range s.channels {
			select {
			case c <- val:
			default:
			}
		}
	})
	
	return s
}

func (s *Stream) Filter(fn func(interface{}) interface{}) *Stream {
	out := &Stream{
		topic: s.topic + "-filtered",
		done:  make(chan struct{}),
	}
	
	inCh := make(chan interface{}, 100)
	s.mu.Lock()
	s.channels = append(s.channels, inCh)
	s.mu.Unlock()
	
	outCh := make(chan interface{}, 100)
	out.channels = append(out.channels, outCh)
	
	go func() {
		for {
			select {
			case <-s.done:
				close(out.done)
				return
			case <-out.done:
				return
			case val, ok := <-inCh:
				if !ok {
					return
				}
				res := fn(val)
				isTruthy := false
				if b, ok := res.(bool); ok {
					isTruthy = b
				} else if res != nil {
					isTruthy = true
				}
				if isTruthy {
					select {
					case outCh <- val:
					default:
					}
				}
			}
		}
	}()
	
	return out
}

func (s *Stream) Window(durStr string) *Stream {
	var dur time.Duration
	var err error
	if strings.HasSuffix(durStr, "m") {
		dVal := strings.TrimSuffix(durStr, "m")
		dur, err = time.ParseDuration(dVal + "m")
	} else if strings.HasSuffix(durStr, "s") {
		dVal := strings.TrimSuffix(durStr, "s")
		dur, err = time.ParseDuration(dVal + "s")
	} else {
		dur, err = time.ParseDuration(durStr)
	}
	if err != nil {
		dur = 5 * time.Second
	}
	
	out := &Stream{
		topic: s.topic + "-windowed",
		done:  make(chan struct{}),
	}
	
	inCh := make(chan interface{}, 100)
	s.mu.Lock()
	s.channels = append(s.channels, inCh)
	s.mu.Unlock()
	
	outCh := make(chan interface{}, 100)
	out.channels = append(out.channels, outCh)
	
	go func() {
		ticker := time.NewTicker(dur)
		defer ticker.Stop()
		
		var batch []interface{}
		var batchMu sync.Mutex
		
		go func() {
			for {
				select {
				case <-s.done:
					return
				case <-out.done:
					return
				case val, ok := <-inCh:
					if !ok {
						return
					}
					batchMu.Lock()
					batch = append(batch, val)
					batchMu.Unlock()
				}
			}
		}()
		
		for {
			select {
			case <-s.done:
				close(out.done)
				return
			case <-out.done:
				return
			case <-ticker.C:
				batchMu.Lock()
				currentBatch := batch
				batch = nil
				batchMu.Unlock()
				
				select {
				case outCh <- currentBatch:
				default:
				}
			}
		}
	}()
	
	return out
}

func (s *Stream) Count() *Stream {
	out := &Stream{
		topic: s.topic + "-counted",
		done:  make(chan struct{}),
	}
	
	inCh := make(chan interface{}, 100)
	s.mu.Lock()
	s.channels = append(s.channels, inCh)
	s.mu.Unlock()
	
	outCh := make(chan interface{}, 100)
	out.channels = append(out.channels, outCh)
	
	go func() {
		accumulated := int64(0)
		for {
			select {
			case <-s.done:
				close(out.done)
				return
			case <-out.done:
				return
			case val, ok := <-inCh:
				if !ok {
					return
				}
				var countVal int64
				if slice, ok := val.([]interface{}); ok {
					countVal = int64(len(slice))
				} else {
					accumulated++
					countVal = accumulated
				}
				select {
				case outCh <- countVal:
				default:
				}
			}
		}
	}()
	
	return out
}

func PublishStream(s interface{}, topic string) {
	streamObj, ok := s.(*Stream)
	if !ok {
		return
	}
	
	inCh := make(chan interface{}, 100)
	streamObj.mu.Lock()
	streamObj.channels = append(streamObj.channels, inCh)
	streamObj.mu.Unlock()
	
	go func() {
		for {
			select {
			case <-streamObj.done:
				return
			case val, ok := <-inCh:
				if !ok {
					return
				}
				Publish(topic, val)
			}
		}
	}()
}
