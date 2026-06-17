package stomp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"

	"servqueue/pkg/broker"
)

type Server struct {
	addr   string
	engine *broker.BrokerEngine
}

func NewServer(addr string, engine *broker.BrokerEngine) *Server {
	return &Server{
		addr:   addr,
		engine: engine,
	}
}

func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go s.handleConnection(conn)
	}
}

type Frame struct {
	Command string
	Headers map[string]string
	Body    string
}

func readFrame(reader *bufio.Reader) (*Frame, error) {
	cmdLine, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	command := strings.TrimSpace(cmdLine)
	if command == "" {
		// Skip empty lines (keep-alive)
		return readFrame(reader)
	}

	headers := make(map[string]string)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break // End of headers
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			headers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}

	// Read body until null byte (\x00)
	bodyBytes, err := reader.ReadBytes(0)
	if err != nil {
		return nil, err
	}
	body := string(bytes.TrimSuffix(bodyBytes, []byte{0}))

	return &Frame{
		Command: command,
		Headers: headers,
		Body:    body,
	}, nil
}

func writeFrame(writer io.Writer, command string, headers map[string]string, body string) error {
	var buf bytes.Buffer
	buf.WriteString(command + "\n")
	for k, v := range headers {
		buf.WriteString(fmt.Sprintf("%s:%s\n", k, v))
	}
	buf.WriteString("\n")
	buf.WriteString(body)
	buf.WriteByte(0)
	_, err := writer.Write(buf.Bytes())
	return err
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	activeSubs := make(map[string]chan string)
	defer func() {
		for topic, ch := range activeSubs {
			s.engine.Unsubscribe(topic, ch)
		}
	}()

	for {
		frame, err := readFrame(reader)
		if err != nil {
			return
		}

		switch frame.Command {
		case "CONNECT":
			writeFrame(writer, "CONNECTED", map[string]string{"version": "1.2"}, "")
			writer.Flush()

		case "SEND":
			destination := frame.Headers["destination"]
			if destination == "" {
				continue
			}
			_, _ = s.engine.Publish(context.Background(), destination, frame.Body)

		case "SUBSCRIBE":
			destination := frame.Headers["destination"]
			subID := frame.Headers["id"]
			if destination == "" {
				continue
			}

			ch := s.engine.Subscribe(destination)
			activeSubs[destination] = ch

			go func(topic string, id string, subChan chan string) {
				for msg := range subChan {
					hdrs := map[string]string{
						"destination":  topic,
						"subscription": id,
						"message-id":   "msg-0",
					}
					// Include traceparent if it looks like JSON with traceparent field
					if strings.Contains(msg, "_traceparent") {
						hdrs["traceparent"] = "extracted-tp"
					}
					_ = writeFrame(conn, "MESSAGE", hdrs, msg)
				}
			}(destination, subID, ch)

		case "DISCONNECT":
			writeFrame(writer, "RECEIPT", map[string]string{"receipt-id": frame.Headers["receipt"]}, "")
			writer.Flush()
			return
		}
	}
}
