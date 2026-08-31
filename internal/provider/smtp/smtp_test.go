package smtp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thorstenkramm/mia/internal/config"
)

func TestSendPasswordRecoveryPlainText(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	messages := make(chan string, 1)
	go serveSMTP(t, listener, messages)
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	var configuration config.Config
	configuration.SMTP.Host = host
	configuration.SMTP.Port = portNumber
	configuration.SMTP.Transport = "plaintext"
	configuration.SMTP.SenderEmail = "mia@example.test"
	configuration.SMTP.SenderName = "MIA Support"
	if err := New(configuration, nil).SendPasswordRecovery(context.Background(), "staff@example.test", "https://mia.test/password-reset#token=token"); err != nil {
		t.Fatal(err)
	}
	message := <-messages
	if !strings.Contains(message, "Content-Type: text/plain; charset=UTF-8") || !strings.Contains(message, "#token=token") || !strings.Contains(message, "MIA Support <mia@example.test>") {
		t.Fatalf("unexpected recovery email: %q", message)
	}
	if !strings.Contains(message, "Date: ") || !strings.Contains(message, "Message-ID: <") || !strings.Contains(message, "@example.test>") {
		t.Fatalf("recovery email lacks Date or Message-ID headers: %q", message)
	}
}

func TestSendPasswordRecoveryClassifiesRejectionAndTransientFailure(t *testing.T) {
	cases := []struct {
		name      string
		rcptReply string
		expected  error
	}{
		{"permanent rejection is definite", "550 no such user\r\n", ErrRejected},
		{"transient rejection stays ambiguous", "450 greylisted, try again later\r\n", ErrAmbiguous},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			configuration := testConfiguration(t, func(listener net.Listener) {
				serveSMTPRcptReply(t, listener, testCase.rcptReply)
			})
			err := New(configuration, nil).SendPasswordRecovery(context.Background(), "staff@example.test", "https://mia.test/password-reset#token=token")
			if !errors.Is(err, testCase.expected) {
				t.Fatalf("classified error = %v, expected %v", err, testCase.expected)
			}
		})
	}
}

func TestSendPasswordRecoveryStalledServerTimesOut(t *testing.T) {
	configuration := testConfiguration(t, func(listener net.Listener) {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer closeConnection(t, connection)
		if _, err := fmt.Fprint(connection, "220 test SMTP\r\n"); err != nil {
			return
		}
		if _, err := io.Copy(io.Discard, connection); err != nil {
			return
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := New(configuration, nil).SendPasswordRecovery(ctx, "staff@example.test", "https://mia.test/password-reset#token=token")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("stalled server error = %v, expected %v", err, ErrTimeout)
	}
}

func TestSendPasswordRecoveryRejectsHeaderInjection(t *testing.T) {
	var configuration config.Config
	configuration.SMTP.Host = "127.0.0.1"
	configuration.SMTP.Port = 25
	configuration.SMTP.Transport = "plaintext"
	configuration.SMTP.SenderEmail = "mia@example.test"
	err := New(configuration, nil).SendPasswordRecovery(context.Background(), "staff@example.test\r\nBcc: other@example.test", "https://mia.test")
	if err == nil || errors.Is(err, ErrAmbiguous) || errors.Is(err, ErrTimeout) || errors.Is(err, ErrRejected) {
		t.Fatalf("header injection error = %v", err)
	}
}

func testConfiguration(t *testing.T, server func(net.Listener)) config.Config {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	})
	go server(listener)
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	var configuration config.Config
	configuration.SMTP.Host = host
	configuration.SMTP.Port = portNumber
	configuration.SMTP.Transport = "plaintext"
	configuration.SMTP.SenderEmail = "mia@example.test"
	return configuration
}

func serveSMTPRcptReply(t *testing.T, listener net.Listener, rcptReply string) {
	t.Helper()
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	defer closeConnection(t, connection)
	reader := bufio.NewReader(connection)
	if _, err := fmt.Fprint(connection, "220 test SMTP\r\n"); err != nil {
		return
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"), strings.HasPrefix(line, "MAIL"):
			_, err = fmt.Fprint(connection, "250 ok\r\n")
		case strings.HasPrefix(line, "RCPT"):
			_, err = fmt.Fprint(connection, rcptReply)
		case strings.HasPrefix(line, "QUIT"):
			if _, err := fmt.Fprint(connection, "221 bye\r\n"); err != nil {
				t.Error(err)
			}
			return
		default:
			return
		}
		if err != nil {
			return
		}
	}
}

func closeConnection(t *testing.T, connection net.Conn) {
	t.Helper()
	if err := connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Error(err)
	}
}

func serveSMTP(t *testing.T, listener net.Listener, messages chan<- string) {
	t.Helper()
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	defer func() {
		if err := connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	}()
	reader := bufio.NewReader(connection)
	if _, err := fmt.Fprint(connection, "220 test SMTP\r\n"); err != nil {
		t.Error(err)
		return
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Error(err)
			}
			return
		}
		switch {
		case strings.HasPrefix(line, "EHLO"):
			_, err = fmt.Fprint(connection, "250 test\r\n")
		case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
			_, err = fmt.Fprint(connection, "250 ok\r\n")
		case strings.HasPrefix(line, "DATA"):
			_, err = fmt.Fprint(connection, "354 send data\r\n")
			if err == nil {
				var body strings.Builder
				for {
					line, err = reader.ReadString('\n')
					if err != nil {
						t.Error(err)
						return
					}
					if line == ".\r\n" {
						break
					}
					body.WriteString(line)
				}
				messages <- body.String()
				_, err = fmt.Fprint(connection, "250 queued\r\n")
			}
		case strings.HasPrefix(line, "QUIT"):
			if _, err := fmt.Fprint(connection, "221 bye\r\n"); err != nil {
				t.Error(err)
			}
			return
		default:
			err = fmt.Errorf("unexpected SMTP command %q", line)
		}
		if err != nil {
			t.Error(err)
			return
		}
	}
}
