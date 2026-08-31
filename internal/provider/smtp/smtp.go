// Package smtp sends MIA's plain-text security emails.
package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	stdsmtp "net/smtp"
	"net/textproto"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thorstenkramm/mia/internal/config"
)

var (
	ErrTimeout   = errors.New("SMTP delivery timed out")
	ErrAmbiguous = errors.New("SMTP delivery outcome is ambiguous")
	ErrRejected  = errors.New("SMTP delivery was rejected")
)

// Client is safe for concurrent use after construction.
type Client struct {
	configuration config.Config
	logger        *slog.Logger
}

func New(configuration config.Config, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{configuration: configuration, logger: logger}
}

// SendPasswordRecovery delivers an English-only plain-text recovery link.
func (client *Client) SendPasswordRecovery(ctx context.Context, recipient, link string) (returnErr error) {
	if strings.ContainsAny(recipient, "\r\n") || strings.ContainsAny(link, "\r\n") {
		return errors.New("unsafe SMTP message value")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	dialer := net.Dialer{Timeout: 10 * time.Second}
	address := net.JoinHostPort(client.configuration.SMTP.Host, fmt.Sprint(client.configuration.SMTP.Port))
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return classifyAmbiguous(err)
	}
	// Close failures never change the delivery outcome: an accepted DATA body is
	// success, and every failure path already carries its classified error.
	defer func() {
		if closeErr := connection.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			client.logger.Warn("closing SMTP connection failed")
		}
	}()
	host := client.configuration.SMTP.Host
	if client.configuration.SMTP.Transport == "implicit_tls" {
		if err := setCommandDeadline(ctx, connection); err != nil {
			return err
		}
		tlsConnection := tls.Client(connection, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		connection = tlsConnection
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			return classifyAmbiguous(err)
		}
	}
	if err := setCommandDeadline(ctx, connection); err != nil {
		return err
	}
	smtpClient, err := stdsmtp.NewClient(connection, host)
	if err != nil {
		return classifyAmbiguous(err)
	}
	if client.configuration.SMTP.Transport == "starttls" {
		if err := setCommandDeadline(ctx, connection); err != nil {
			return err
		}
		if err := smtpClient.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return classifyAmbiguous(err)
		}
	}
	if client.configuration.SMTP.Username != "" {
		if err := setCommandDeadline(ctx, connection); err != nil {
			return err
		}
		authenticator := stdsmtp.PlainAuth("", client.configuration.SMTP.Username, client.configuration.SMTP.Password, host)
		if client.configuration.SMTP.Transport == "plaintext" {
			authenticator = plainAuth{username: client.configuration.SMTP.Username, password: client.configuration.SMTP.Password}
		}
		if err := smtpClient.Auth(authenticator); err != nil {
			return classifyRejected(err)
		}
	}
	if err := setCommandDeadline(ctx, connection); err != nil {
		return err
	}
	if err := smtpClient.Mail(client.configuration.SMTP.SenderEmail); err != nil {
		return classifyRejected(err)
	}
	if err := setCommandDeadline(ctx, connection); err != nil {
		return err
	}
	if err := smtpClient.Rcpt(recipient); err != nil {
		return classifyRejected(err)
	}
	if err := setCommandDeadline(ctx, connection); err != nil {
		return err
	}
	writer, err := smtpClient.Data()
	if err != nil {
		return classifyRejected(err)
	}
	from, err := fromHeader(client.configuration.SMTP.SenderName, client.configuration.SMTP.SenderEmail)
	if err != nil {
		return err
	}
	message := "From: " + from + "\r\n" +
		"To: " + recipient + "\r\n" +
		"Subject: Reset your MIA password\r\n" +
		"Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n" +
		"Message-ID: <" + uuid.NewString() + "@" + messageIDHost(client.configuration.SMTP.SenderEmail) + ">\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" +
		"Use this link to reset your MIA password:\n\n" + link + "\n\n" +
		"This link expires in 30 minutes. If you did not request it, you can ignore this email.\n"
	if _, err := writer.Write([]byte(message)); err != nil {
		return classifyAmbiguous(err)
	}
	if err := writer.Close(); err != nil {
		return classifyRejected(err)
	}
	// A successful DATA close means the server accepted the message; QUIT is not delivery confirmation.
	return nil
}

func setCommandDeadline(ctx context.Context, connection net.Conn) error {
	deadline := time.Now().Add(10 * time.Second)
	if total, ok := ctx.Deadline(); ok && total.Before(deadline) {
		deadline = total
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set SMTP command deadline: %w", err)
	}
	return nil
}

func classifyAmbiguous(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrTimeout
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return ErrTimeout
	}
	return ErrAmbiguous
}

// classifyRejected treats only permanent 5xx replies as definite rejection.
// Transient 4xx replies (greylisting, temporary limits) stay ambiguous so the
// challenge is not invalidated for a message the server may yet accept.
func classifyRejected(err error) error {
	var response *textproto.Error
	if errors.As(err, &response) && response.Code >= 500 && response.Code < 600 {
		return ErrRejected
	}
	return classifyAmbiguous(err)
}

func messageIDHost(senderEmail string) string {
	if at := strings.LastIndex(senderEmail, "@"); at >= 0 && at < len(senderEmail)-1 {
		return senderEmail[at+1:]
	}
	return "mia.invalid"
}

func fromHeader(name, email string) (string, error) {
	if strings.ContainsAny(name, "\r\n") {
		return "", errors.New("unsafe SMTP sender name")
	}
	if name == "" {
		return email, nil
	}
	return mime.QEncoding.Encode("utf-8", name) + " <" + email + ">", nil
}

// plainAuth deliberately bypasses net/smtp's refusal to send AUTH PLAIN over
// unencrypted connections. Configuring smtp.transport = "plaintext" together
// with credentials is a documented operator decision; see
// docs/server-configuration.md.
type plainAuth struct{ username, password string }

func (auth plainAuth) Start(*stdsmtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + auth.username + "\x00" + auth.password), nil
}
func (plainAuth) Next([]byte, bool) ([]byte, error) { return nil, nil }
