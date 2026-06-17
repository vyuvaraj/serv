package stomp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"

	"servqueue/pkg/broker"
)

type Server struct {
	addr      string
	engine    *broker.BrokerEngine
	username  string
	passcode  string
	tlsCert   string
	tlsKey    string
}

func NewServer(addr string, engine *broker.BrokerEngine, username, passcode, tlsCert, tlsKey string) *Server {
	return &Server{
		addr:     addr,
		engine:   engine,
		username: username,
		passcode: passcode,
		tlsCert:  tlsCert,
		tlsKey:   tlsKey,
	}
}

func (s *Server) Start() error {
	var listener net.Listener
	var err error

	if s.tlsCert != "" && s.tlsKey != "" {
		cert, certErr := tls.LoadX509KeyPair(s.tlsCert, s.tlsKey)
		if certErr != nil {
			return fmt.Errorf("tls: failed to load certificates: %w", certErr)
		}
		cfg := &tls.Config{Certificates: []tls.Certificate{cert}}
		listener, err = tls.Listen("tcp", s.addr, cfg)
	} else {
		listener, err = net.Listen("tcp", s.addr)
	}

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

	authenticated := false

	// If no username/passcode is required, default to authenticated
	if s.username == "" && s.passcode == "" {
		authenticated = true
	}

	for {
		frame, err := readFrame(reader)
		if err != nil {
			return
		}

		if frame.Command == "CONNECT" {
			login := frame.Headers["login"]
			passcode := frame.Headers["passcode"]

			if !authenticated && (login != s.username || passcode != s.passcode) {
				_ = writeFrame(writer, "ERROR", map[string]string{"message": "Authentication failed"}, "Invalid credentials")
				writer.Flush()
				return
			}

			authenticated = true
			writeFrame(writer, "CONNECTED", map[string]string{"version": "1.2"}, "")
			writer.Flush()
			continue
		}

		if !authenticated {
			_ = writeFrame(writer, "ERROR", map[string]string{"message": "Not authenticated"}, "Send CONNECT frame first")
			writer.Flush()
			return
		}

		switch frame.Command {
		case "SEND":
			destination := frame.Headers["destination"]
			if destination == "" {
				continue
			}

			// Capture traceparent header for context propagation
			ctx := context.Background()
			if tp, exists := frame.Headers["traceparent"]; exists {
				ctx = context.WithValue(ctx, "traceparent", tp)
			}

			_, _ = s.engine.Publish(ctx, destination, frame.Body)

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
