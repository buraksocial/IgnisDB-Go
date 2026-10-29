// Package client provides a Go client library for IgnisDB-Go.
package client

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Client is a Go client for IgnisDB-Go.
type Client struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
}

// Connect connects to an IgnisDB-Go server.
func Connect(addr string) (*Client, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Client{
		conn:   conn,
		reader: bufio.NewReader(conn),
		writer: bufio.NewWriter(conn),
	}, nil
}

// Close closes the connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Set sets a key-value pair.
func (c *Client) Set(key, value string) error {
	err := c.sendCommand("SET", key, value)
	if err != nil {
		return err
	}
	_, err = c.readResponse()
	return err
}

// Get gets a value by key.
func (c *Client) Get(key string) (string, error) {
	err := c.sendCommand("GET", key)
	if err != nil {
		return "", err
	}
	resp, err := c.readResponse()
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", nil
	}
	if s, ok := resp.(string); ok {
		return s, nil
	}
	return fmt.Sprintf("%v", resp), nil
}

// Del deletes one or more keys.
func (c *Client) Del(keys ...string) (int64, error) {
	args := make([]string, len(keys))
	copy(args, keys)
	err := c.sendCommand("DEL", args...)
	if err != nil {
		return 0, err
	}
	resp, err := c.readResponse()
	if err != nil {
		return 0, err
	}
	if n, ok := resp.(int64); ok {
		return n, nil
	}
	return 0, nil
}

// Ping sends a PING command.
func (c *Client) Ping() (string, error) {
	err := c.sendCommand("PING")
	if err != nil {
		return "", err
	}
	resp, err := c.readResponse()
	if err != nil {
		return "", err
	}
	if s, ok := resp.(string); ok {
		return s, nil
	}
	return fmt.Sprintf("%v", resp), nil
}

func (c *Client) sendCommand(cmd string, args ...string) error {
	totalArgs := 1 + len(args)
	c.writer.WriteString(fmt.Sprintf("*%d\r\n", totalArgs))
	c.writer.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(cmd), cmd))
	for _, arg := range args {
		c.writer.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(arg), arg))
	}
	return c.writer.Flush()
}

func (c *Client) readResponse() (interface{}, error) {
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")

	if len(line) == 0 {
		return nil, fmt.Errorf("empty response")
	}

	switch line[0] {
	case '+':
		return line[1:], nil
	case '-':
		return nil, fmt.Errorf("%s", line[1:])
	case ':':
		n, err := strconv.ParseInt(line[1:], 10, 64)
		return n, err
	case '$':
		length, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if length == -1 {
			return nil, nil
		}
		data := make([]byte, length)
		_, err = c.reader.Read(data)
		if err != nil {
			return nil, err
		}
		c.reader.ReadString('\n') // consume trailing \r\n
		return string(data), nil
	case '*':
		count, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		items := make([]interface{}, count)
		for i := 0; i < count; i++ {
			items[i], err = c.readResponse()
			if err != nil {
				return nil, err
			}
		}
		return items, nil
	default:
		return line, nil
	}
}
