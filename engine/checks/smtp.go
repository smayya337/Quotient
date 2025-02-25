package checks

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/smtp"
	"os"
	"slices"
	"strings"
	"time"
)

/// BEGIN SMTP AUTH LOGIN SUPPORT

func PlainOrLoginAuth(username, password, host string) smtp.Auth {
	return &plainOrLoginAuth{username: username, password: password, host: host}
}

type plainOrLoginAuth struct {
	username   string
	password   string
	host       string
	authMethod string
}

func (a *plainOrLoginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !slices.Contains(server.Auth, "PLAIN") {
		a.authMethod = "LOGIN"
		return a.authMethod, nil, nil
	} else {
		a.authMethod = "PLAIN"
		resp := []byte("\x00" + a.username + "\x00" + a.password)
		return a.authMethod, resp, nil
	}
}

func (a *plainOrLoginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}

	if a.authMethod == "PLAIN" {
		// We've already sent everything.
		return nil, errors.New("unexpected server challenge")
	}

	switch {
	case bytes.Equal(fromServer, []byte("Username:")):
		return []byte(a.username), nil
	case bytes.Equal(fromServer, []byte("Password:")):
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("unexpected server challenge: %s", fromServer)
	}
}

/// END SMTP AUTH LOGIN SUPPORT

type Smtp struct {
	Service
	Encrypted bool
	Domain    string
	Fortunes  []string
}

func (c Smtp) Run(teamID uint, teamIdentifier string, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		fortunes, err := os.ReadFile("/usr/share/games/fortunes/fortunes")
		if err != nil {
			checkResult.Error = "failed to load fortune file (/usr/share/games/fortunes/fortunes)"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		c.Fortunes = strings.Split(string(fortunes), "\n%\n")
		if len(c.Fortunes) == 0 {
			checkResult.Error = "failed to load fortune file (/usr/share/games/fortunes/fortunes)"
			checkResult.Debug = "no fortunes found"
			response <- checkResult
			return
		}

		// Create a dialer
		dialer := net.Dialer{
			Timeout: time.Duration(c.Timeout) * time.Second,
		}

		fortune := c.Fortunes[rand.Intn(len(c.Fortunes))]
		words := strings.Fields(fortune)
		subject := ""
		if len(words) <= 3 {
			subject = fortune
		} else {
			selected := make([]string, 3)
			for i := 0; i < 3; i++ {
				selected[i] = words[rand.Intn(len(words))]
			}
			subject = strings.Join(selected, " ")
		}

		// ***********************************************
		// Set up custom auth for bypassing net/smtp protections
		username, password, err := c.getCreds(teamID)
		if err != nil {
			checkResult.Error = "error getting creds"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		toUser, _, err := c.getCreds(teamID)
		if err != nil {
			checkResult.Error = "error getting creds"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		auth := PlainOrLoginAuth(username+c.Domain, password, c.Target)
		// ***********************************************

		if c.Domain != "" {
			username = username + c.Domain
			toUser = toUser + c.Domain
		}

		// The good way to do auth
		// auth := smtp.PlainAuth("", d.Username, d.Password, d.Host)
		// Create TLS config
		tlsConfig := tls.Config{
			InsecureSkipVerify: true,
		}

		// Declare these for the below if block
		var conn net.Conn

		if c.Encrypted {
			conn, err = tls.DialWithDialer(&dialer, "tcp", fmt.Sprintf("%s:%d", c.Target, c.Port), &tlsConfig)
		} else {
			conn, err = dialer.DialContext(context.TODO(), "tcp", fmt.Sprintf("%s:%d", c.Target, c.Port))
		}
		if err != nil {
			checkResult.Error = "connection to server failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		defer conn.Close()

		// Create smtp client
		sconn, err := smtp.NewClient(conn, c.Target)
		if err != nil {
			checkResult.Error = "smtp client creation failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		defer sconn.Quit()

		// Login
		if len(c.CredLists) > 0 {
			err = sconn.Auth(auth)
			if err != nil {
				checkResult.Error = "login failed for " + username + ":" + password
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}
		}

		// Set the sender
		err = sconn.Mail(username)
		if err != nil {
			checkResult.Error = "setting sender failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		// Set the receiver
		err = sconn.Rcpt(toUser)
		if err != nil {
			checkResult.Error = "setting receiver failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		// Create email writer
		wc, err := sconn.Data()
		if err != nil {
			checkResult.Error = "creating email writer failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		defer wc.Close()

		body := fmt.Sprintf("Subject: %s\n\n%s\n\n", subject, fortune)

		// Write the body
		_, err = fmt.Fprintf(wc, body)
		if err != nil {
			checkResult.Error = "writing body failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		checkResult.Status = true
		checkResult.Debug = "successfully wrote '" + body + "' to " + toUser + " from " + username
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, resultsChan, definition)
}

func (c *Smtp) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}
	if c.Display == "" {
		c.Display = "smtp"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	if c.Port == 0 {
		c.Port = 25
	}

	return nil
}
